package raknet

import (
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
)

// UpstreamPacketListener allows for a custom PacketListener implementation.
type UpstreamPacketListener interface {
	ListenPacket(network, address string) (net.PacketConn, error)
}

// ListenConfig may be used to pass additional configuration to a Listener.
type ListenConfig struct {
	// ErrorLog is a logger that errors from packet decoding are logged to. It
	// may be set to a logger that simply discards the messages. The default
	// value is slog.Default().
	ErrorLog *slog.Logger

	// UpstreamPacketListener adds an abstraction for net.ListenPacket.
	UpstreamPacketListener UpstreamPacketListener
}

// Listener implements a RakNet connection listener. It follows the same
// methods as those implemented by the TCPListener in the net package. Listener
// implements the net.Listener interface.
type Listener struct {
	h *listenerConnectionHandler

	once   sync.Once
	closed chan struct{}

	// log is a logger that errors from packet decoding are logged to. It may be
	// set to a logger that simply discards the messages.
	log *slog.Logger

	conn net.PacketConn
	// incoming is a channel of incoming connections. Connections that end up in
	// here will also end up in the connections map.
	incoming chan *Conn

	// connections is a map of currently active connections, indexed by their
	// address.
	connections sync.Map

	// id is a random server ID generated upon starting listening. It is used
	// several times throughout the connection sequence of RakNet.
	id int64

	// pongData is a byte slice of data that is sent in an unconnected pong
	// packet each time the client sends and unconnected ping to the server.
	pongData atomic.Pointer[[]byte]
}

// listenerID holds the next ID to use for a Listener.
var listenerID = rand.Int63()

// Listen listens on the address passed and returns a listener that may be used
// to accept connections. If not successful, an error is returned. The address
// follows the same rules as those defined in the net.TCPListen() function.
// Specific features of the listener may be modified once it is returned, such
// as the used log and/or the accepted protocol.
func (l ListenConfig) Listen(address string) (*Listener, error) {
	var conn net.PacketConn
	var err error

	if l.UpstreamPacketListener == nil {
		conn, err = net.ListenPacket("udp", address)
	} else {
		conn, err = l.UpstreamPacketListener.ListenPacket("udp", address)
	}
	if err != nil {
		return nil, &net.OpError{Op: "listen", Net: "raknet", Source: nil, Addr: nil, Err: err}
	}
	listener := &Listener{
		conn:     conn,
		incoming: make(chan *Conn),
		closed:   make(chan struct{}),
		log:      slog.Default(),
		id:       atomic.AddInt64(&listenerID, 1),
	}
	listener.h = &listenerConnectionHandler{l: listener}
	if l.ErrorLog != nil {
		listener.log = l.ErrorLog
	}

	go listener.listen()
	return listener, nil
}

// Listen listens on the address passed and returns a listener that may be used
// to accept connections. If not successful, an error is returned. The address
// follows the same rules as those defined in the net.TCPListen() function.
// Specific features of the listener may be modified once it is returned, such
// as the used log and/or the accepted protocol.
func Listen(address string) (*Listener, error) {
	var lc ListenConfig
	return lc.Listen(address)
}

// Accept blocks until a connection can be accepted by the listener. If
// successful, Accept returns a connection that is ready to send and receive
// data. If not successful, a nil listener is returned and an error describing
// the problem.
func (listener *Listener) Accept() (net.Conn, error) {
	conn, ok := <-listener.incoming
	if !ok {
		return nil, &net.OpError{Op: "accept", Net: "raknet", Source: nil, Addr: nil, Err: errListenerClosed}
	}
	return conn, nil
}

// Addr returns the address the Listener is bound to and listening for
// connections on.
func (listener *Listener) Addr() net.Addr {
	return listener.conn.LocalAddr()
}

// Close closes the listener so that it may be cleaned up. It makes sure the
// goroutine handling incoming packets is able to be freed.
func (listener *Listener) Close() error {
	var err error
	listener.once.Do(func() {
		close(listener.closed)
		err = listener.conn.Close()
	})
	return err
}

// PongData sets the pong data that is used to respond with when a client sends
// a ping. It usually holds game specific data that is used to display in a
// server list. If a data slice is set with a size bigger than math.MaxInt16,
// the function panics.
func (listener *Listener) PongData(data []byte) {
	if len(data) > math.MaxInt16 {
		panic(fmt.Sprintf("pong data: must be no longer than %v bytes, got %v", math.MaxInt16, len(data)))
	}
	listener.pongData.Store(&data)
}

// ID returns the unique ID of the listener. This ID is usually used by a
// client to identify a specific server during a single session.
func (listener *Listener) ID() int64 {
	return listener.id
}

// listen continuously reads from the listener's UDP connection, until closed
// has a value in it.
func (listener *Listener) listen() {
	// Create a buffer with the maximum size a UDP packet sent over RakNet is
	// allowed to have. We can re-use this buffer for each packet.
	b := make([]byte, 1500)
	for {
		n, addr, err := listener.conn.ReadFrom(b)
		if err != nil {
			close(listener.incoming)
			return
		} else if n == 0 {
			continue
		}
		if err := listener.handle(b[:n], addr); err != nil {
			listener.log.Error("listener: handle packet: "+err.Error(), "address", addr.String())
		}
	}
}

// handle handles an incoming packet in buffer b from the address passed. If
// not successful, an error is returned describing the issue.
func (listener *Listener) handle(b []byte, addr net.Addr) error {
	value, found := listener.connections.Load(resolve(addr))
	if !found {
		return listener.h.handleUnconnected(b, addr)
	}
	conn := value.(*Conn)
	select {
	case <-conn.closed:
		// Connection was closed already.
		return nil
	default:
		if err := conn.receive(b); err != nil {
			conn.closeImmediately()
			return err
		}
		return nil
	}
}
