package raknet

import (
	"context"
	"errors"
	"fmt"
	"github.com/sandertv/go-raknet/internal"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync/atomic"
	"time"

	"github.com/sandertv/go-raknet/internal/message"
)

// UpstreamDialer is an interface for anything compatible with net.Dialer.
type UpstreamDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Ping sends a ping to an address and returns the response obtained. If
// successful, a non-nil response byte slice containing the data is returned.
// If the ping failed, an error is returned describing the failure. Note that
// the packet sent to the server may be lost due to the nature of UDP. If this
// is the case, an error is returned which implies a timeout occurred. Ping
// will time out after 5 seconds.
func Ping(address string) (response []byte, err error) {
	var d Dialer
	return d.Ping(address)
}

// PingTimeout sends a ping to an address and returns the response obtained. If
// successful, a non-nil response byte slice containing the data is returned.
// If the ping failed, an error is returned describing the failure. Note that
// the packet sent to the server may be lost due to the nature of UDP. If this
// is the case, an error is returned which implies a timeout occurred.
// PingTimeout will time out after the duration passed.
func PingTimeout(address string, timeout time.Duration) ([]byte, error) {
	var d Dialer
	return d.PingTimeout(address, timeout)
}

// PingContext sends a ping to an address and returns the response obtained. If
// successful, a non-nil response byte slice containing the data is returned.
// If the ping failed, an error is returned describing the failure. Note that
// the packet sent to the server may be lost due to the nature of UDP. If this
// is the case, PingContext could last indefinitely, hence a timeout should
// always be attached to the context passed. PingContext cancels as soon as the
// deadline expires.
func PingContext(ctx context.Context, address string) (response []byte, err error) {
	var d Dialer
	return d.PingContext(ctx, address)
}

// Dial attempts to dial a RakNet connection to the address passed. The address
// may be either an IP address or a hostname, combined with a port that is
// separated with ':'. Dial will attempt to dial a connection within 10
// seconds. If not all packets are received after that, the connection will
// time out and an error will be returned. Dial fills out a Dialer struct with a
// default error logger.
func Dial(address string) (*Conn, error) {
	var d Dialer
	return d.Dial(address)
}

// DialTimeout attempts to dial a RakNet connection to the address passed. The
// address may be either an IP address or a hostname, combined with a port that
// is separated with ':'. DialTimeout will attempt to dial a connection within
// the timeout duration passed. If not all packets are received after that, the
// connection will time out and an error will be returned.
func DialTimeout(address string, timeout time.Duration) (*Conn, error) {
	var d Dialer
	return d.DialTimeout(address, timeout)
}

// DialContext attempts to dial a RakNet connection to the address passed. The
// address may be either an IP address or a hostname, combined with a port that
// is separated with ':'. DialContext will use the deadline (ctx.Deadline) of
// the context.Context passed for the maximum amount of time that the dialing
// can take. DialContext will terminate as soon as possible when the
// context.Context is closed.
func DialContext(ctx context.Context, address string) (*Conn, error) {
	var d Dialer
	return d.DialContext(ctx, address)
}

// Dialer allows dialing a RakNet connection with specific configuration, such
// as the protocol version of the connection and the logger used.
type Dialer struct {
	// ErrorLog is a logger that errors from packet decoding are logged to. By
	// default, ErrorLog is set to a new slog.Logger with a slog.Handler that
	// is always disabled. Error messages are thus not logged by default.
	ErrorLog *slog.Logger

	// UpstreamDialer is a dialer that will override the default dialer for
	// opening outgoing connections. The default is a net.Dial("udp", ...).
	UpstreamDialer UpstreamDialer
}

// Ping sends a ping to an address and returns the response obtained. If
// successful, a non-nil response byte slice containing the data is returned.
// If the ping failed, an error is returned describing the failure. Note that
// the packet sent to the server may be lost due to the nature of UDP. If this
// is the case, an error is returned which implies a timeout occurred. Ping
// will time out after 5 seconds.
func (dialer Dialer) Ping(address string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	return dialer.PingContext(ctx, address)
}

// PingTimeout sends a ping to an address and returns the response obtained. If
// successful, a non-nil response byte slice containing the data is returned.
// If the ping failed, an error is returned describing the failure. Note that
// the packet sent to the server may be lost due to the nature of UDP. If this
// is the case, an error is returned which implies a timeout occurred.
// PingTimeout will time out after the duration passed.
func (dialer Dialer) PingTimeout(address string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return dialer.PingContext(ctx, address)
}

// PingContext sends a ping to an address and returns the response obtained. If
// successful, a non-nil response byte slice containing the data is returned.
// If the ping failed, an error is returned describing the failure. Note that
// the packet sent to the server may be lost due to the nature of UDP. If this
// is the case, PingContext could last indefinitely, hence a timeout should
// always be attached to the context passed. PingContext cancels as soon as the
// deadline expires.
func (dialer Dialer) PingContext(ctx context.Context, address string) (response []byte, err error) {
	conn, err := dialer.dial(ctx, address)
	if err != nil {
		return nil, dialer.error("ping", err)
	}
	defer conn.Close()

	data, _ := (&message.UnconnectedPing{PingTime: timestamp(), ClientGUID: atomic.AddInt64(&dialerID, 1)}).MarshalBinary()
	if _, err := conn.Write(data); err != nil {
		return nil, dialer.error("ping", err)
	}

	data = make([]byte, 1492)
	n, err := conn.Read(data)
	if err != nil {
		return nil, dialer.error("ping", err)
	}
	if n == 0 || data[0] != message.IDUnconnectedPong {
		return nil, dialer.error("ping", fmt.Errorf("non-pong packet found (id = %v)", data[0]))
	}
	pong := &message.UnconnectedPong{}
	if err := pong.UnmarshalBinary(data[1:n]); err != nil {
		return nil, dialer.error("ping", fmt.Errorf("read unconnected pong: %w", err))
	}
	return pong.Data, nil
}

// error wraps the error passed resulting from an operation in a *net.OpError.
func (dialer Dialer) error(op string, err error) *net.OpError {
	return &net.OpError{Op: op, Net: "raknet", Source: nil, Addr: nil, Err: err}
}

// dial dials a connection to an address and assigns the deadline of the context
// to the connection.
func (dialer Dialer) dial(ctx context.Context, address string) (net.Conn, error) {
	dial := (&net.Dialer{}).DialContext
	if dialer.UpstreamDialer != nil {
		dial = dialer.UpstreamDialer.DialContext
	}
	conn, err := dial(ctx, "udp", address)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	return conn, nil
}

// dialerID is a counter used to produce an ID for the client.
var dialerID = rand.Int64()

// Dial attempts to dial a RakNet connection to the address passed. The address
// may be either an IP address or a hostname, combined with a port that is
// separated with ':'. Dial will attempt to dial a connection within 10
// seconds. If not all packets are received after that, the connection will
// timeout and an error will be returned.
func (dialer Dialer) Dial(address string) (*Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	return dialer.DialContext(ctx, address)
}

// DialTimeout attempts to dial a RakNet connection to the address passed. The
// address may be either an IP address or a hostname, combined with a port that
// is separated with ':'. DialTimeout will attempt to dial a connection within
// the timeout duration passed. If not all packets are received after that, the
// connection will time out and an error will be returned.
func (dialer Dialer) DialTimeout(address string, timeout time.Duration) (*Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return dialer.DialContext(ctx, address)
}

// DialContext attempts to dial a RakNet connection to the address passed. The
// address may be either an IP address or a hostname, combined with a port that
// is separated with ':'. DialContext will use the deadline (ctx.Deadline) of
// the context.Context passed for the maximum amount of time that the dialing
// can take. DialContext will terminate as soon as possible when the
// context.Context is closed.
func (dialer Dialer) DialContext(ctx context.Context, address string) (*Conn, error) {
	if dialer.ErrorLog == nil {
		dialer.ErrorLog = slog.New(internal.DiscardHandler{})
	}

	conn, err := dialer.dial(ctx, address)
	if err != nil {
		return nil, dialer.error("dial", err)
	}
	dialer.ErrorLog = dialer.ErrorLog.With("src", "dialer", "raddr", conn.RemoteAddr().String())

	cs := &connState{conn: conn, raddr: conn.RemoteAddr(), id: atomic.AddInt64(&dialerID, 1), ticker: time.NewTicker(time.Second / 2)}
	defer cs.ticker.Stop()
	if err = cs.discoverMTU(ctx); err != nil {
		return nil, dialer.error("dial", err)
	} else if err = cs.openConnection(ctx); err != nil {
		return nil, dialer.error("dial", err)
	}

	return dialer.connect(ctx, cs)
}

// dial finishes the RakNet connection sequence and returns a Conn if
// successful.
func (dialer Dialer) connect(ctx context.Context, state *connState) (*Conn, error) {
	conn := newConn(internal.ConnToPacketConn(state.conn), state.raddr, state.mtu, dialerConnectionHandler{l: dialer.ErrorLog})
	if err := conn.send((&message.ConnectionRequest{ClientGUID: state.id, RequestTime: timestamp()})); err != nil {
		return nil, dialer.error("dial", fmt.Errorf("send connection request: %w", err))
	}

	go dialer.clientListen(conn, state.conn)

	select {
	case <-conn.connected:
		// Remove connection deadline.
		_ = conn.conn.SetDeadline(time.Time{})
		return conn, nil
	case <-ctx.Done():
		_ = conn.Close()
		return nil, dialer.error("dial", ctx.Err())
	}
}

// clientListen makes the RakNet connection passed listen as a client for
// packets received in the connection passed.
func (dialer Dialer) clientListen(rakConn *Conn, conn net.Conn) {
	// Create a buffer with the maximum size a UDP packet sent over RakNet is
	// allowed to have. We can re-use this buffer for each packet.
	b := make([]byte, rakConn.effectiveMTU())
	for {
		n, err := conn.Read(b)
		if err == nil && n != 0 {
			err = rakConn.receive(b[:n])
		}
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Errors reading a packet other than the connection being
			// closed may be worth logging.
			dialer.ErrorLog.Error("handle packet: " + err.Error())
		}
	}
}

// connState represents a state of a connection before the connection is
// finalised. It holds some data collected during the connection.
type connState struct {
	conn  net.Conn
	raddr net.Addr
	id    int64

	// mtu is the final MTU size found by sending an open connection request
	// 1 packet. It is the MTU size sent by the server.
	mtu uint16

	serverSecurity bool
	cookie         uint32

	ticker *time.Ticker
}

var mtuSizes = []uint16{1492, 1200, 576}

// discoverMTU starts discovering an MTU size, the maximum packet size we
// can send, by sending multiple open connection request 1 packets to the
// server with a decreasing MTU size padding.
func (state *connState) discoverMTU(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go state.request1(ctx, mtuSizes)

	b := make([]byte, 1492)
	for {
		// Start reading in a loop so that we can find an open connection reply
		// 1 packet.
		n, err := state.conn.Read(b)
		if err != nil || n == 0 {
			state.close()
			return err
		}
		switch b[0] {
		case message.IDOpenConnectionReply1:
			response := &message.OpenConnectionReply1{}
			if err := response.UnmarshalBinary(b[1:n]); err != nil {
				return fmt.Errorf("read open connection reply 1: %w", err)
			}
			state.serverSecurity, state.cookie = response.ServerHasSecurity, response.Cookie
			if response.ServerGUID == 0 || response.MTU < 400 || response.MTU > 1500 {
				// This is an awful hack we cooked up to deal with OVH 'DDoS'
				// protection. For some reason they send a broken MTU size
				// first. Sending a Request2 followed by a Request1 deals with
				// this.
				state.openConnectionRequest2(response.MTU)
				continue
			}
			state.mtu = response.MTU
			return nil
		case message.IDIncompatibleProtocolVersion:
			response := &message.IncompatibleProtocolVersion{}
			if err := response.UnmarshalBinary(b[1:n]); err != nil {
				return fmt.Errorf("read incompatible protocol version: %w", err)
			}
			return fmt.Errorf("mismatched protocol: client protocol = %v, server protocol = %v", protocolVersion, response.ServerProtocol)
		}
	}
}

// request1 sends a message.OpenConnectionRequest1 three times for each mtu
// size passed, spaced by 500ms.
func (state *connState) request1(ctx context.Context, sizes []uint16) {
	state.ticker.Reset(time.Second / 2)
	for _, size := range sizes {
		for range 3 {
			state.openConnectionRequest1(size)
			select {
			case <-state.ticker.C:
				continue
			case <-ctx.Done():
				return
			}
		}
	}
}

// openConnection sends open connection request 2 packets continuously
// until it receives an open connection reply 2 packet from the server.
func (state *connState) openConnection(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go state.request2(ctx, state.mtu)

	b := make([]byte, 1492)
	for {
		// Start reading in a loop so that we can find open connection reply 2
		// packets.
		n, err := state.conn.Read(b)
		if err != nil || n == 0 {
			state.close()
			return err
		}
		if b[0] != message.IDOpenConnectionReply2 {
			continue
		}
		pk := &message.OpenConnectionReply2{}
		if err = pk.UnmarshalBinary(b[1:n]); err != nil {
			return fmt.Errorf("read open connection reply 2: %w", err)
		}
		state.mtu = pk.MTU
		return nil
	}
}

// request2 continuously sends a message.OpenConnectionRequest2 every 500ms.
func (state *connState) request2(ctx context.Context, mtu uint16) {
	state.ticker.Reset(time.Second / 2)
	for {
		state.openConnectionRequest2(mtu)
		select {
		case <-state.ticker.C:
			continue
		case <-ctx.Done():
			return
		}
	}
}

// openConnectionRequest1 sends an open connection request 1 packet to the
// server. If not successful, an error is returned.
func (state *connState) openConnectionRequest1(mtu uint16) {
	data, _ := (&message.OpenConnectionRequest1{ClientProtocol: protocolVersion, MTU: mtu}).MarshalBinary()
	_, _ = state.conn.Write(data)
}

// openConnectionRequest2 sends an open connection request 2 packet to the
// server. If not successful, an error is returned.
func (state *connState) openConnectionRequest2(mtu uint16) {
	data, _ := (&message.OpenConnectionRequest2{
		ServerAddress:     resolve(state.raddr),
		MTU:               mtu,
		ClientGUID:        state.id,
		ServerHasSecurity: state.serverSecurity,
		Cookie:            state.cookie,
	}).MarshalBinary()
	_, _ = state.conn.Write(data)
}

// close closes the underlying connection.
func (state *connState) close() {
	_ = state.conn.Close()
}
