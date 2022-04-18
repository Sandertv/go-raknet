package raknet

import (
	"bytes"
	"context"
	"fmt"
	"github.com/df-mc/atomic"
	"golang.org/x/net/proxy"
	"log"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/sandertv/go-raknet/internal/message"
)

// Ping sends a ping to an address and returns the response obtained. If successful, a non-nil response byte
// slice containing the data is returned. If the ping failed, an error is returned describing the failure.
// Note that the packet sent to the server may be lost due to the nature of UDP. If this is the case, an error
// is returned which implies a timeout occurred.
// Ping will timeout after 5 seconds.
func Ping(address string) (response []byte, err error) {
	var d Dialer
	return d.Ping(address)
}

// PingTimeout sends a ping to an address and returns the response obtained. If successful, a non-nil response
// byte slice containing the data is returned. If the ping failed, an error is returned describing the
// failure.
// Note that the packet sent to the server may be lost due to the nature of UDP. If this is the case, an error
// is returned which implies a timeout occurred.
// PingTimeout will time out after the duration passed.
func PingTimeout(address string, timeout time.Duration) ([]byte, error) {
	var d Dialer
	return d.PingTimeout(address, timeout)
}

// PingContext sends a ping to an address and returns the response obtained. If successful, a non-nil response
// byte slice containing the data is returned. If the ping failed, an error is returned describing the
// failure.
// Note that the packet sent to the server may be lost due to the nature of UDP. If this is the case,
// PingContext could last indefinitely, hence a timeout should always be attached to the context passed.
// PingContext cancels as soon as the deadline expires.
func PingContext(ctx context.Context, address string) (response []byte, err error) {
	var d Dialer
	return d.PingContext(ctx, address)
}

// Dial attempts to dial a RakNet connection to the address passed. The address may be either an IP address
// or a hostname, combined with a port that is separated with ':'.
// Dial will attempt to dial a connection within 10 seconds. If not all packets are received after that, the
// connection will timeout and an error will be returned.
// Dial fills out a Dialer struct with a default error logger.
func Dial(address string) (*Conn, error) {
	var d Dialer
	return d.Dial(address)
}

// DialTimeout attempts to dial a RakNet connection to the address passed. The address may be either an IP
// address or a hostname, combined with a port that is separated with ':'.
// DialTimeout will attempt to dial a connection within the timeout duration passed. If not all packets are
// received after that, the connection will timeout and an error will be returned.
func DialTimeout(address string, timeout time.Duration) (*Conn, error) {
	var d Dialer
	return d.DialTimeout(address, timeout)
}

// DialContext attempts to dial a RakNet connection to the address passed. The address may be either an IP
// address or a hostname, combined with a port that is separated with ':'.
// DialContext will use the deadline (ctx.Deadline) of the context.Context passed for the maximum amount of
// time that the dialing can acknowledge. DialContext will terminate as soon as possible when the context.Context is
// closed.
func DialContext(ctx context.Context, address string) (*Conn, error) {
	var d Dialer
	return d.DialContext(ctx, address)
}

// Dialer allows dialing a RakNet connection with specific configuration, such as the protocol version of the
// connection and the logger used.
type Dialer struct {
	// ErrorLog is a logger that errors from packet decoding are logged to. It may be set to a logger that
	// simply discards the messages.
	ErrorLog *log.Logger

	// UpstreamDialer is a dialer that will override the default dialer for opening outgoing connections.
	UpstreamDialer proxy.Dialer
}

// Ping sends a ping to an address and returns the response obtained. If successful, a non-nil response byte
// slice containing the data is returned. If the ping failed, an error is returned describing the failure.
// Note that the packet sent to the server may be lost due to the nature of UDP. If this is the case, an error
// is returned which implies a timeout occurred.
// Ping will timeout after 5 seconds.
func (dialer Dialer) Ping(address string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	return dialer.PingContext(ctx, address)
}

// PingTimeout sends a ping to an address and returns the response obtained. If successful, a non-nil response
// byte slice containing the data is returned. If the ping failed, an error is returned describing the
// failure.
// Note that the packet sent to the server may be lost due to the nature of UDP. If this is the case, an error
// is returned which implies a timeout occurred.
// PingTimeout will time out after the duration passed.
func (dialer Dialer) PingTimeout(address string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return dialer.PingContext(ctx, address)
}

// PingContext sends a ping to an address and returns the response obtained. If successful, a non-nil response
// byte slice containing the data is returned. If the ping failed, an error is returned describing the
// failure.
// Note that the packet sent to the server may be lost due to the nature of UDP. If this is the case,
// PingContext could last indefinitely, hence a timeout should always be attached to the context passed.
// PingContext cancels as soon as the deadline expires.
func (dialer Dialer) PingContext(ctx context.Context, address string) (response []byte, err error) {
	var (
		dial = net.Dial
		done = make(chan struct{})
		data = make([]byte, 1500)
		buf  = new(bytes.Buffer)
		pong = new(message.UnconnectedPong)
	)
	if dialer.UpstreamDialer != nil {
		dial = dialer.UpstreamDialer.Dial
	}
	conn, err := dial("udp", address)
	if err != nil {
		return nil, netErr("ping", err)
	}
	defer close(done)
	go ctxClose(ctx, done, conn)

	(&message.UnconnectedPing{SendTimestamp: timestamp(), ClientGUID: dialerID.Inc()}).Write(buf)
	if _, err := conn.Write(buf.Bytes()); err != nil {
		return nil, netErr("ping", ctx.Err())
	}
	buf.Reset()

	n, err := conn.Read(data)
	if err != nil {
		return nil, netErr("ping", err)
	}

	_, _ = buf.Write(data[:n])
	id, _ := buf.ReadByte()
	if err = pong.Read(buf); err != nil || id != message.IDUnconnectedPong {
		return nil, netErr("ping", fmt.Errorf("invalid unconnected pong: %w", err))
	}
	_ = conn.Close()
	return pong.Data, nil
}

// dialerID is a counter used to produce an ID for the client.
var dialerID = atomic.NewInt64(rand.New(rand.NewSource(time.Now().Unix())).Int63())

// Dial attempts to dial a RakNet connection to the address passed. The address may be either an IP address
// or a hostname, combined with a port that is separated with ':'.
// Dial will attempt to dial a connection within 10 seconds. If not all packets are received after that, the
// connection will timeout and an error will be returned.
func (dialer Dialer) Dial(address string) (*Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	return dialer.DialContext(ctx, address)
}

// DialTimeout attempts to dial a RakNet connection to the address passed. The address may be either an IP
// address or a hostname, combined with a port that is separated with ':'.
// DialTimeout will attempt to dial a connection within the timeout duration passed. If not all packets are
// received after that, the connection will timeout and an error will be returned.
func (dialer Dialer) DialTimeout(address string, timeout time.Duration) (*Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return dialer.DialContext(ctx, address)
}

// DialContext attempts to dial a RakNet connection to the address passed. The address may be either an IP
// address or a hostname, combined with a port that is separated with ':'.
// DialContext will use the deadline (ctx.Deadline) of the context.Context passed for the maximum amount of
// time that the dialing can acknowledge. DialContext will terminate as soon as possible when the context.Context is
// closed.
func (dialer Dialer) DialContext(ctx context.Context, address string) (*Conn, error) {
	dial := net.Dial
	if dialer.UpstreamDialer != nil {
		dial = dialer.UpstreamDialer.Dial
	}
	if dialer.ErrorLog == nil {
		dialer.ErrorLog = log.New(os.Stderr, "", log.LstdFlags)
	}
	udp, err := dial("udp", address)
	if err != nil {
		return nil, err
	}

	done := make(chan struct{})
	defer close(done)
	go ctxClose(ctx, done, udp)

	state := &connState{
		id:      dialerID.Inc(),
		mtuSize: 1492,
		conn:    udp,
	}
	if err := state.discoverMTUSize(); err != nil {
		return nil, netErr("dial", err)
	}
	if err := state.openConnectionRequest(); err != nil {
		return nil, netErr("dial", err)
	}

	conn := newConn(wrappedConn{PacketConn: udp.(net.PacketConn)}, udp.RemoteAddr(), state.mtuSize)
	conn.close = func() { _ = udp.Close() }
	if err = conn.requestConnection(state.id); err != nil {
		return nil, netErr("dial", err)
	}

	go clientListen(conn, udp, dialer.ErrorLog)
	select {
	case <-conn.connected:
		_ = udp.SetDeadline(time.Unix(1, 0))
		return conn, nil
	case <-ctx.Done():
		_ = conn.Close()
		return nil, netErr("dial", ctx.Err())
	}
}

// wrappedCon wraps around a 'pre-connected' UDP connection. Its only purpose is to wrap around WriteTo and
// make it call Write instead.
type wrappedConn struct {
	net.PacketConn
}

// WriteTo wraps around net.PacketConn to replace functionality of WriteTo with Write. It is used to be able
// to re-use the functionality in raknet.Conn.
func (conn wrappedConn) WriteTo(b []byte, _ net.Addr) (n int, err error) {
	return conn.PacketConn.(net.Conn).Write(b)
}

// clientListen makes the RakNet connection passed listen as a client for packets received in the connection
// passed.
func clientListen(rakConn *Conn, conn net.Conn, errorLog *log.Logger) {
	// Create a buffer with the maximum size a UDP packet sent over RakNet is allowed to have. We can re-use
	// this buffer for each packet.
	b := make([]byte, 1500)
	buf := bytes.NewBuffer(b[:0])
	for {
		n, err := conn.Read(b)
		if err != nil {
			// The connection was closed, so we can return from the function without logging the error.
			return
		}
		buf.Write(b[:n])
		if err := rakConn.receive(buf); err != nil {
			errorLog.Printf("client: error handling packet: %v\n", err)
			_ = rakConn.Close()
		}
		buf.Reset()
	}
}

// connState represents a state of a connection before the connection is finalised. It holds some data
// collected during the connection.
type connState struct {
	conn net.Conn
	id   int64

	// mtuSize is the current MTU size of the connection. This value is negotiated by both ends of the connection
	// during this stage.
	mtuSize uint16
}

// discoverMTUSize starts discovering an MTU size, the maximum packet size we can send, by sending multiple
// open connection request 1 packets to the server with a decreasing MTU size padding.
func (state *connState) discoverMTUSize() error {
	return state.poll(time.Second/2, state.sendOpenConnectionRequest1, func(id byte, buf *bytes.Buffer) (bool, error) {
		switch id {
		case message.IDOpenConnectionReply1:
			response := &message.OpenConnectionReply1{}
			if err := response.Read(buf); err != nil {
				return false, fmt.Errorf("error reading open connection reply 1: %v", err)
			}
			if response.ServerPreferredMTUSize < 400 || response.ServerPreferredMTUSize > 1500 {
				return false, fmt.Errorf("invalid server mtu size %v", response.ServerPreferredMTUSize)
			}
			state.mtuSize = response.ServerPreferredMTUSize
			return false, nil
		case message.IDIncompatibleProtocolVersion:
			response := &message.IncompatibleProtocolVersion{}
			_ = response.Read(buf)
			return false, fmt.Errorf("mismatched protocol: client protocol = %v, server protocol = %v", currentProtocol, response.ServerProtocol)
		}
		return true, nil
	})
}

// openConnectionRequest sends open connection request 2 packets continuously until it receives an open
// connection reply 2 packet from the server.
func (state *connState) openConnectionRequest() error {
	return state.poll(time.Second/2, state.sendOpenConnectionRequest2, func(id byte, buf *bytes.Buffer) (bool, error) {
		if id != message.IDOpenConnectionReply2 {
			return true, nil
		}
		reply := &message.OpenConnectionReply2{}
		if err := reply.Read(buf); err != nil {
			return false, fmt.Errorf("error reading open connection reply 2: %w", err)
		}
		state.mtuSize = reply.MTUSize
		return false, nil
	})
}

// poll calls the send function at least once every dur. The function f passed is called for every packet read after
// the send function was called. poll ends as soon as the underlying connection is closed, or if the f function returns
// retry=false + err=nil.
func (state *connState) poll(dur time.Duration, send func(mtu uint16) error, f func(id byte, buf *bytes.Buffer) (retry bool, err error)) error {
	b := make([]byte, 1500)
	for {
		if err := send(state.mtuSize); err != nil {
			return err
		}
		_ = state.conn.SetReadDeadline(time.Now().Add(dur))

		n, err := state.conn.Read(b)
		if opErr, ok := err.(net.Error); ok && opErr.Timeout() {
			continue
		} else if err != nil {
			return err
		}
		buffer := bytes.NewBuffer(b[:n])
		id, err := buffer.ReadByte()
		if err != nil {
			continue
		}
		if retry, err := f(id, buffer); err != nil || !retry {
			return err
		}
	}
}

// sendOpenConnectionRequest2 sends an open connection request 2 packet to the server. If not successful, an
// error is returned.
func (state *connState) sendOpenConnectionRequest2(mtu uint16) error {
	b := new(bytes.Buffer)
	(&message.OpenConnectionRequest2{ServerAddress: *state.conn.RemoteAddr().(*net.UDPAddr), ClientPreferredMTUSize: mtu, ClientGUID: state.id}).Write(b)
	_, err := state.conn.Write(b.Bytes())
	return err
}

// sendOpenConnectionRequest1 sends an open connection request 1 packet to the server. If not successful, an
// error is returned.
func (state *connState) sendOpenConnectionRequest1(mtu uint16) error {
	b := new(bytes.Buffer)
	(&message.OpenConnectionRequest1{Protocol: currentProtocol, MaximumSizeNotDropped: mtu}).Write(b)
	_, err := state.conn.Write(b.Bytes())
	state.mtuSize -= 46
	return err
}

// netErr wraps an operation and error into a *net.OpError.
func netErr(op string, err error) *net.OpError {
	return &net.OpError{Op: op, Net: "raknet", Source: nil, Addr: nil, Err: err}
}

// ctxClose closes a net.Conn if the Context passed is closed before the channel passed is.
func ctxClose(ctx context.Context, done <-chan struct{}, conn net.Conn) {
	select {
	case <-done:
	case <-ctx.Done():
		_ = conn.Close()
	}
}
