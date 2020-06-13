package raknet

import (
	"bytes"
	"context"
	"fmt"
	"github.com/sandertv/go-raknet/internal/message"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Listener implements a RakNet connection listener. It follows the same methods as those implemented by the
// TCPListener in the net package.
// Listener implements the net.Listener interface.
type Listener struct {
	// ErrorLog is a logger that errors from packet decoding are logged to. It may be set to a logger that
	// simply discards the messages.
	ErrorLog *log.Logger

	conn net.PacketConn
	// incoming is a channel of incoming connections. Connections that end up in here will also end up in
	// the connections map.
	incoming chan *Conn

	// connections is a map of currently active connections, indexed by their address.
	connections sync.Map

	closeCtx context.Context
	close    context.CancelFunc

	// id is a random server ID generated upon starting listening. It is used several times throughout the
	// connection sequence of RakNet.
	id int64

	// pongData is a byte slice of data that is sent in an unconnected pong packet each time the client sends
	// and unconnected ping to the server.
	pongData atomic.Value
}

// Listen listens on the address passed and returns a listener that may be used to accept connections. If not
// successful, an error is returned.
// The address follows the same rules as those defined in the net.TCPListen() function.
// Specific features of the listener may be modified once it is returned, such as the used ErrorLog and/or the
// accepted protocol.
func Listen(address string) (*Listener, error) {
	conn, err := net.ListenPacket("udp", address)
	if err != nil {
		return nil, fmt.Errorf("error creating UDP listener: %v", err)
	}

	// Seed the global rand so we can get a random ID.
	rand.Seed(time.Now().Unix())
	ctx, cancel := context.WithCancel(context.Background())

	listener := &Listener{
		ErrorLog: log.New(os.Stderr, "", log.LstdFlags),
		conn:     conn,
		incoming: make(chan *Conn, 128),
		closeCtx: ctx,
		close:    cancel,
		id:       rand.Int63(),
	}
	listener.pongData.Store([]byte{})
	go listener.listen()

	return listener, nil
}

// Accept blocks until a connection can be accepted by the listener. If successful, Accept returns a
// connection that is ready to send and receive data. If not successful, a nil listener is returned and an error
// describing the problem.
func (listener *Listener) Accept() (net.Conn, error) {
accept:
	conn, ok := <-listener.incoming
	if !ok {
		return nil, fmt.Errorf("error accepting connection: listener closed")
	}
	select {
	case <-listener.closeCtx.Done():
		return nil, fmt.Errorf("error accepting connection: listener closed")
	case <-conn.completingSequence.Done():
		go func() {
			<-conn.closeCtx.Done()
			// Insert the boolean back in the channel so that other readers of the channel also receive
			// the signal.
			listener.connections.Delete(conn.addr.String())
		}()
		return conn, nil
	case <-time.After(time.Second * 10):
		// It took too long to complete this connection. We closeCtx it and go back to accepting.
		_ = conn.Close()
		goto accept
	}
}

// Addr returns the address the Listener is bound to and listening for connections on.
func (listener *Listener) Addr() net.Addr {
	return listener.conn.LocalAddr()
}

// Close closes the listener so that it may be cleaned up. It makes sure the goroutine handling incoming
// packets is able to be freed.
func (listener *Listener) Close() error {
	listener.close()

	var err error
	listener.connections.Range(func(key, value interface{}) bool {
		conn := value.(*Conn)
		if closeErr := conn.Close(); err != nil {
			err = fmt.Errorf("error closing conn %v: %v", conn.addr, closeErr)
		}
		return true
	})
	if err != nil {
		return err
	}
	if err := listener.conn.Close(); err != nil {
		return fmt.Errorf("error closing UDP listener: %v", err)
	}
	return nil
}

// PongData sets the pong data that is used to respond with when a client sends a ping. It usually holds game
// specific data that is used to display in a server list.
// If a data slice is set with a size bigger than math.MaxInt16, the function panics.
func (listener *Listener) PongData(data []byte) {
	if len(data) > math.MaxInt16 {
		panic(fmt.Sprintf("error setting pong data: pong data must not be longer than %v", math.MaxInt16))
	}
	listener.pongData.Store(data)
}

// HijackPong hijacks the pong response from a server at an address passed. The listener passed will
// continuously update its pong data by hijacking the pong data of the server at the address.
// The hijack will last until the listener is shut down.
// If the address passed could not be resolved, an error is returned.
// Calling HijackPong means that any current and future pong data set using listener.PongData is overwritten
// each update.
func (listener *Listener) HijackPong(address string) error {
	if _, err := net.ResolveUDPAddr("udp", address); err != nil {
		return fmt.Errorf("error resolving UDP address: %v", err)
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				data, err := Ping(address)
				if err != nil {
					// It's okay if these packets are lost sometimes. There's no need to log this.
					continue
				}
				//noinspection SpellCheckingInspection
				if string(data[:4]) == "MCPE" {
					fragments := bytes.Split(data, []byte{';'})
					for len(fragments) < 9 {
						// Append to the fragments if it's not at least 9 elements long.
						fragments = append(fragments, nil)
					}

					fragments = fragments[:9]
					fragments[6] = []byte(strconv.Itoa(int(listener.id)))
					fragments[7] = []byte("Proxy")
					fragments[8] = []byte{}

					listener.PongData(bytes.Join(fragments, []byte{';'}))
				} else {
					listener.PongData(data)
				}
			case <-listener.closeCtx.Done():
				return
			}
		}
	}()
	return nil
}

// ID returns the unique ID of the listener. This ID is usually used by a client to identify a specific
// server during a single session.
func (listener *Listener) ID() int64 {
	return listener.id
}

// listen continuously reads from the listener's UDP connection, until closeCtx has a value in it.
func (listener *Listener) listen() {
	// Create a buffer with the maximum size a UDP packet sent over RakNet is allowed to have. We can re-use
	// this buffer for each packet.
	b := make([]byte, 1500)
	for {
		n, addr, err := listener.conn.ReadFrom(b)
		if err != nil {
			close(listener.incoming)
			return
		}
		buffer := b[:n]

		// Technically we should not re-use the same byte slice after its ownership has been taken by the
		// buffer, but we can do this anyway because we copy the data later.
		if err := listener.handle(bytes.NewBuffer(buffer), addr); err != nil {
			listener.ErrorLog.Printf("error handling packet (rakAddr = %v): %v\n", addr, err)
		}
	}
}

// handle handles an incoming packet in buffer b from the address passed. If not successful, an error is
// returned describing the issue.
func (listener *Listener) handle(b *bytes.Buffer, addr net.Addr) error {
	value, found := listener.connections.Load(addr.String())
	if !found {
		// If there was no session yet, it means the packet is an offline message. It is not contained in a
		// datagram.
		packetID, err := b.ReadByte()
		if err != nil {
			return fmt.Errorf("error reading packet ID byte: %v", err)
		}
		switch packetID {
		case message.IDUnconnectedPing:
			return listener.handleUnconnectedPing(b, addr)
		case message.IDOpenConnectionRequest1:
			return listener.handleOpenConnectionRequest1(b, addr)
		case message.IDOpenConnectionRequest2:
			return listener.handleOpenConnectionRequest2(b, addr)
		default:
			// In some cases, the client will keep trying to send datagrams while it has already timed out. In
			// this case, we should not print an error.
			if packetID&bitFlagValid == 0 {
				return fmt.Errorf("unknown packet received (%x): %x", packetID, b.Bytes())
			}
		}
		return nil
	}
	return value.(*Conn).receive(b)
}

// handleOpenConnectionRequest2 handles an open connection request 2 packet stored in buffer b, coming from
// an address addr.
func (listener *Listener) handleOpenConnectionRequest2(b *bytes.Buffer, addr net.Addr) error {
	packet := &message.OpenConnectionRequest2{}
	if err := packet.Read(b); err != nil {
		return fmt.Errorf("error reading open connection request 2: %v", err)
	}
	b.Reset()

	(&message.OpenConnectionReply2{ServerGUID: listener.id, ClientAddress: *addr.(*net.UDPAddr), MTUSize: packet.MTUSize}).Write(b)
	if _, err := listener.conn.WriteTo(b.Bytes(), addr); err != nil {
		return fmt.Errorf("error sending open connection reply 2: %v", err)
	}

	conn := newConn(listener.conn, addr, packet.MTUSize, packet.ClientGUID, false)
	listener.connections.Store(addr.String(), conn)

	// Add the connection to the incoming channel so that a caller of Accept() can receive it.
	listener.incoming <- conn

	return nil
}

// handleOpenConnectionRequest1 handles an open connection request 1 packet stored in buffer b, coming from
// an address addr.
func (listener *Listener) handleOpenConnectionRequest1(b *bytes.Buffer, addr net.Addr) error {
	packet := &message.OpenConnectionRequest1{}
	if err := packet.Read(b); err != nil {
		return fmt.Errorf("error reading open connection request 1: %v", err)
	}
	b.Reset()
	mtuSize := packet.MTUSize

	if packet.Protocol != currentProtocol {
		(&message.IncompatibleProtocolVersion{ServerGUID: listener.id, ServerProtocol: currentProtocol}).Write(b)
		_, _ = listener.conn.WriteTo(b.Bytes(), addr)
		return fmt.Errorf("error handling open connection request 1: incompatible protocol version %v (listener protocol = %v)", packet.Protocol, currentProtocol)
	}

	(&message.OpenConnectionReply1{ServerGUID: listener.id, Secure: false, MTUSize: mtuSize}).Write(b)
	_, err := listener.conn.WriteTo(b.Bytes(), addr)
	return err
}

// handleUnconnectedPing handles an unconnected ping packet stored in buffer b, coming from an address addr.
func (listener *Listener) handleUnconnectedPing(b *bytes.Buffer, addr net.Addr) error {
	pk := &message.UnconnectedPing{}
	if err := pk.Read(b); err != nil {
		return fmt.Errorf("error reading unconnected ping: %v", err)
	}
	b.Reset()

	(&message.UnconnectedPong{ServerGUID: listener.id, SendTimestamp: pk.SendTimestamp, Data: listener.pongData.Load().([]byte)}).Write(b)
	_, err := listener.conn.WriteTo(b.Bytes(), addr)
	return err
}

// timestamp returns a timestamp in milliseconds.
func timestamp() int64 {
	return time.Now().UnixNano() / int64(time.Second)
}
