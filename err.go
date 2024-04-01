package raknet

import (
	"errors"
	"net"
)

var (
	errBufferTooSmall = errors.New("a message sent was larger than the buffer used to receive the message into")
	errListenerClosed = errors.New("use of closed listener")
)

// error wraps the error passed into a net.OpError with the op as operation and
// returns it, or nil if the error passed is nil.
func (conn *Conn) error(err error, op string) error {
	if err == nil {
		return nil
	}
	return &net.OpError{
		Op:     op,
		Net:    "raknet",
		Source: conn.LocalAddr(),
		Addr:   conn.raddr,
		Err:    err,
	}
}
