package raknet

import (
	"errors"
	"net"
	"strings"
)

var (
	errBufferTooSmall = errors.New("a message sent was larger than the buffer used to receive the message into")
	errListenerClosed = errors.New("use of closed listener")
)

// ErrConnectionClosed checks if the error passed was an error caused by reading from a Conn of which the
// connection was closed.
func ErrConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), net.ErrClosed.Error())
}

// wrap wraps the error passed into a net.OpError with the op as operation and returns it, or nil if the error
// passed is nil.
func (conn *Conn) wrap(err error, op string) error {
	if err == nil {
		return nil
	}
	return &net.OpError{
		Op:     op,
		Net:    "raknet",
		Source: conn.LocalAddr(),
		Addr:   conn.RemoteAddr(),
		Err:    err,
	}
}
