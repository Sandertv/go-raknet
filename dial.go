package raknet

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/sandertv/go-raknet/internal/message"
)

// UpstreamDialer is an interface for anything compatible with net.Dialer.
type UpstreamDialer interface {
	Dial(network, address string) (net.Conn, error)
}

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
// time that the dialing can take. DialContext will terminate as soon as possible when the context.Context is
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
	UpstreamDialer UpstreamDialer
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
	var conn net.Conn

	if dialer.UpstreamDialer == nil {
		conn, err = net.Dial("udp", address)
	} else {
		conn, err = dialer.UpstreamDialer.Dial("udp", address)
	}
	if err != nil {
		return nil, &net.OpError{Op: "ping", Net: "raknet", Source: nil, Addr: nil, Err: err}
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			_ = conn.Close()
		}
	}()
	actual := func(e error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return e
	}

	buffer := bytes.NewBuffer(nil)
	(&message.UnconnectedPing{SendTimestamp: timestamp(), ClientGUID: atomic.AddInt64(&dialerID, 1)}).Write(buffer)
	if _, err := conn.Write(buffer.Bytes()); err != nil {
		return nil, &net.OpError{Op: "ping", Net: "raknet", Source: nil, Addr: nil, Err: actual(err)}
	}
	buffer.Reset()

	data := make([]byte, 1492)
	n, err := conn.Read(data)
	if err != nil {
		return nil, &net.OpError{Op: "ping", Net: "raknet", Source: nil, Addr: nil, Err: actual(err)}
	}
	data = data[:n]

	_, _ = buffer.Write(data)
	if b, err := buffer.ReadByte(); err != nil || b != message.IDUnconnectedPong {
		return nil, &net.OpError{Op: "ping", Net: "raknet", Source: nil, Addr: nil, Err: fmt.Errorf("non-pong packet found: %w", err)}
	}
	pong := &message.UnconnectedPong{}
	if err := pong.Read(buffer); err != nil {
		return nil, &net.OpError{Op: "ping", Net: "raknet", Source: nil, Addr: nil, Err: fmt.Errorf("invalid unconnected pong: %w", err)}
	}
	_ = conn.Close()
	return pong.Data, nil
}

// dialerID is a counter used to produce an ID for the client.
var dialerID = rand.New(rand.NewSource(time.Now().Unix())).Int63()

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
// time that the dialing can take. DialContext will terminate as soon as possible when the context.Context is
// closed.
func (dialer Dialer) DialContext(ctx context.Context, address string) (*Conn, error) {
	var udpConn net.Conn
	var err error

	if dialer.UpstreamDialer == nil {
		udpConn, err = net.Dial("udp", address)
	} else {
		udpConn, err = dialer.UpstreamDialer.Dial("udp", address)
	}
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: "raknet", Source: nil, Addr: nil, Err: err}
	}
	packetConn := udpConn.(net.PacketConn)

	if deadline, ok := ctx.Deadline(); ok {
		_ = packetConn.SetDeadline(deadline)
	}

	id := atomic.AddInt64(&dialerID, 1)
	if dialer.ErrorLog == nil {
		dialer.ErrorLog = log.New(os.Stderr, "", log.LstdFlags)
	}
	state := &connState{
		conn:               udpConn,
		remoteAddr:         udpConn.RemoteAddr(),
		discoveringMTUSize: 1492,
		id:                 id,
	}
	wrap := func(ctx context.Context, err error) error {
		return &net.OpError{Op: "dial", Net: "raknet", Source: nil, Addr: nil, Err: err}
	}

	if err := state.discoverMTUSize(ctx); err != nil {
		return nil, wrap(ctx, err)
	} else if err := state.openConnectionRequest(ctx); err != nil {
		return nil, wrap(ctx, err)
	}

	conn := newConnWithLimits(&wrappedConn{PacketConn: packetConn}, udpConn.RemoteAddr(), uint16(atomic.LoadUint32(&state.mtuSize)), false)
	conn.close = func() {
		// We have to make the Conn call this method explicitly because it must not close the connection
		// established by the Listener. (This would close the entire listener.)
		_ = udpConn.Close()
	}
	if err := conn.requestConnection(id); err != nil {
		return nil, wrap(ctx, err)
	}

	go clientListen(conn, udpConn, dialer.ErrorLog)
	select {
	case <-conn.connected:
		_ = packetConn.SetDeadline(time.Time{})
		return conn, nil
	case <-ctx.Done():
		_ = conn.Close()
		return nil, wrap(ctx, ctx.Err())
	}
}

// wrappedCon wraps around a 'pre-connected' UDP connection. Its only purpose is to wrap around WriteTo and
// make it call Write instead.
type wrappedConn struct {
	net.PacketConn
}

// WriteTo wraps around net.PacketConn to replace functionality of WriteTo with Write. It is used to be able
// to re-use the functionality in raknet.Conn.
func (conn *wrappedConn) WriteTo(b []byte, _ net.Addr) (n int, err error) {
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
			if ErrConnectionClosed(err) {
				// The connection was closed, so we can return from the function without logging the error.
				return
			}
			errorLog.Printf("client: error reading from Conn: %v", err)
			return
		}
		buf.Write(b[:n])
		if err := rakConn.receive(buf); err != nil {
			errorLog.Printf("error handling packet: %v\n", err)
		}
		buf.Reset()
	}
}

// connState represents a state of a connection before the connection is finalised. It holds some data
// collected during the connection.
type connState struct {
	conn       net.Conn
	remoteAddr net.Addr
	id         int64

	// mtuSize is the final MTU size found by sending open connection request 1 packets. It is the MTU size
	// sent by the server.
	mtuSize uint32

	// discoveringMTUSize is the current MTU size 'discovered'. This MTU size decreases the more the open
	// connection request 1 is sent, so that the max packet size can be discovered.
	discoveringMTUSize uint16
}

// openConnectionRequest sends open connection request 2 packets continuously until it receives an open
// connection reply 2 packet from the server.
func (state *connState) openConnectionRequest(ctx context.Context) (e error) {
	ticker := time.NewTicker(time.Second / 2)
	defer ticker.Stop()

	stop := make(chan bool)
	defer func() {
		close(stop)
	}()
	// Use an intermediate channel to start the ticker immediately.
	c := make(chan struct{}, 1)
	c <- struct{}{}
	go func() {
		for {
			select {
			case <-c:
				if err := state.sendOpenConnectionRequest2(uint16(atomic.LoadUint32(&state.mtuSize))); err != nil {
					e = err
					return
				}
			case <-ticker.C:
				c <- struct{}{}
			case <-stop:
				return
			case <-ctx.Done():
				_ = state.conn.Close()
				return
			}
		}
	}()

	b := make([]byte, 1492)
	for {
		// Start reading in a loop so that we can find open connection reply 2 packets.
		n, err := state.conn.Read(b)
		if err != nil {
			return err
		}
		buffer := bytes.NewBuffer(b[:n])
		id, err := buffer.ReadByte()
		if err != nil {
			return fmt.Errorf("error reading packet ID: %v", err)
		}
		if id != message.IDOpenConnectionReply2 {
			// We got a packet, but the packet was not an open connection reply 2 packet. We simply discard it
			// and continue reading.
			continue
		}
		reply := &message.OpenConnectionReply2{}
		if err := reply.Read(buffer); err != nil {
			return fmt.Errorf("error reading open connection reply 2: %v", err)
		}
		atomic.StoreUint32(&state.mtuSize, uint32(reply.MTUSize))
		return
	}
}

// discoverMTUSize starts discovering an MTU size, the maximum packet size we can send, by sending multiple
// open connection request 1 packets to the server with a decreasing MTU size padding.
func (state *connState) discoverMTUSize(ctx context.Context) (e error) {
	ticker := time.NewTicker(time.Second / 2)
	defer ticker.Stop()
	var staticMTU uint16

	stop := make(chan struct{})
	defer func() {
		close(stop)
	}()
	// Use an intermediate channel to start the ticker immediately.
	c := make(chan struct{}, 1)
	c <- struct{}{}
	go func() {
		for {
			select {
			case <-c:
				mtu := state.discoveringMTUSize
				if staticMTU != 0 {
					mtu = staticMTU
				}
				if err := state.sendOpenConnectionRequest1(mtu); err != nil {
					e = err
					return
				}
				if staticMTU == 0 {
					// Each half second we decrease the MTU size by 40. This means that in 10 seconds, we have an MTU
					// size of 692. This is a little above the actual RakNet minimum, but that should not be an issue.
					state.discoveringMTUSize -= 40
				}
			case <-ticker.C:
				c <- struct{}{}
			case <-stop:
				return
			case <-ctx.Done():
				_ = state.conn.Close()
				return
			}
		}
	}()

	b := make([]byte, 1492)
	for {
		// Start reading in a loop so that we can find open connection reply 1 packets.
		n, err := state.conn.Read(b)
		if err != nil {
			return err
		}
		buffer := bytes.NewBuffer(b[:n])
		id, err := buffer.ReadByte()
		if err != nil {
			return fmt.Errorf("error reading packet ID: %v", err)
		}
		switch id {
		case message.IDOpenConnectionReply1:
			response := &message.OpenConnectionReply1{}
			if err := response.Read(buffer); err != nil {
				return fmt.Errorf("error reading open connection reply 1: %v", err)
			}
			if response.ServerPreferredMTUSize < 400 || response.ServerPreferredMTUSize > 1500 {
				// This is an awful hack we cooked up to deal with OVH 'DDoS' protection. For some reason they
				// send a broken MTU size first. Sending a Request2 followed by a Request1 deals with this.
				_ = state.sendOpenConnectionRequest2(response.ServerPreferredMTUSize)
				staticMTU = state.discoveringMTUSize + 40
				continue
			}
			atomic.StoreUint32(&state.mtuSize, uint32(response.ServerPreferredMTUSize))
			return
		case message.IDIncompatibleProtocolVersion:
			response := &message.IncompatibleProtocolVersion{}
			if err := response.Read(buffer); err != nil {
				return fmt.Errorf("error reading incompatible protocol version: %v", err)
			}
			return fmt.Errorf("mismatched protocol: client protocol = %v, server protocol = %v", currentProtocol, response.ServerProtocol)
		}
	}
}

// sendOpenConnectionRequest2 sends an open connection request 2 packet to the server. If not successful, an
// error is returned.
func (state *connState) sendOpenConnectionRequest2(mtu uint16) error {
	b := bytes.NewBuffer(nil)
	(&message.OpenConnectionRequest2{ServerAddress: *state.remoteAddr.(*net.UDPAddr), ClientPreferredMTUSize: mtu, ClientGUID: state.id}).Write(b)
	_, err := state.conn.Write(b.Bytes())
	return err
}

// sendOpenConnectionRequest1 sends an open connection request 1 packet to the server. If not successful, an
// error is returned.
func (state *connState) sendOpenConnectionRequest1(mtu uint16) error {
	b := bytes.NewBuffer(nil)
	(&message.OpenConnectionRequest1{Protocol: currentProtocol, MaximumSizeNotDropped: mtu}).Write(b)
	_, err := state.conn.Write(b.Bytes())
	return err
}
