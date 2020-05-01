package raknet

import (
	"bytes"
	"fmt"
	"github.com/sandertv/go-raknet/internal/message"
	"log"
	"math/rand"
	"net"
	"os"
	"time"
)

// Dial attempts to dial a RakNet connection to the address passed. The address may be either an IP address
// or a hostname, combined with a port that is separated with ':'.
// Dial will attempt to dial a connection within 10 seconds. If not all packets are received after that, the
// connection will timeout and an error will be returned.
// Dial fills out a Dialer struct with a default error logger and raknet.currentProtocol as protocol.
func Dial(address string) (*Conn, error) {
	return Dialer{}.Dial(address)
}

// Ping sends a ping to an address and returns the response obtained. If successful, a non-nil response byte
// slice containing the data is returned. If the ping failed, an error is returned describing the failure.
// Note that the packet sent to the server may be lost due to the nature of UDP. If this is the case, an error
// is returned which implies a timeout occurred.
// sendPing fills out a Dialer struct with raknet.currentProtocol as protocol.
func Ping(address string) (response []byte, err error) {
	return Dialer{}.Ping(address)
}

// Dialer allows dialing a RakNet connection with specific configuration, such as the protocol version of the
// connection and the logger used.
type Dialer struct {
	// ErrorLog is a logger that errors from packet decoding are logged to. It may be set to a logger that
	// simply discards the messages.
	ErrorLog *log.Logger
}

// Ping sends a ping to an address and returns the response obtained. If successful, a non-nil response byte
// slice containing the data is returned. If the ping failed, an error is returned describing the failure.
// Note that the packet sent to the server may be lost due to the nature of UDP. If this is the case, an error
// is returned which implies a timeout occurred.
func (dialer Dialer) Ping(address string) (response []byte, err error) {
	conn, err := net.Dial("udp", address)
	if err != nil {
		return nil, fmt.Errorf("error dialing UDP conn: %v", err)
	}

	buffer := bytes.NewBuffer(nil)
	(&message.UnconnectedPing{SendTimestamp: timestamp(), ClientGUID: rand.New(rand.NewSource(time.Now().Unix())).Int63()}).Write(buffer)
	if _, err := conn.Write(buffer.Bytes()); err != nil {
		return nil, fmt.Errorf("error sending unconnected ping: %v", err)
	}
	buffer.Reset()

	// Set a read deadline so that we get a timeout if the server doesn't respond to us.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second * 5))

	data := make([]byte, 1492)
	n, err := conn.Read(data)
	if err != nil {
		return nil, fmt.Errorf("timeout reading the response: %v", err)
	}
	data = data[:n]

	_, _ = buffer.Write(data)
	if b, err := buffer.ReadByte(); err != nil {
		return nil, fmt.Errorf("error reading unconnected pong ID: %v", err)
	} else if b != message.IDUnconnectedPong {
		return nil, fmt.Errorf("response pong did not have unconnected pong ID")
	}
	pong := &message.UnconnectedPong{}
	if err := pong.Read(buffer); err != nil {
		return nil, fmt.Errorf("error decoding unconnected pong: %v", err)
	}
	_ = conn.Close()
	return pong.Data, nil
}

// Dial attempts to dial a RakNet connection to the address passed. The address may be either an IP address
// or a hostname, combined with a port that is separated with ':'.
// Dial will attempt to dial a connection within 10 seconds. If not all packets are received after that, the
// connection will timeout and an error will be returned.
// Dial will fill out any values left as their empty values with the default values of those fields.
func (dialer Dialer) Dial(address string) (*Conn, error) {
	udpConn, err := net.Dial("udp", address)
	if err != nil {
		return nil, fmt.Errorf("error dialing UDP conn: %v", err)
	}
	packetConn := udpConn.(net.PacketConn)

	_ = udpConn.SetReadDeadline(time.Now().Add(time.Second * 10))

	if dialer.ErrorLog == nil {
		dialer.ErrorLog = log.New(os.Stderr, "", log.LstdFlags)
	}

	id := rand.New(rand.NewSource(time.Now().Unix())).Int63()
	state := &connState{
		conn:               udpConn,
		remoteAddr:         udpConn.RemoteAddr(),
		discoveringMTUSize: 1492,
		id:                 id,
	}
	if err := state.discoverMTUSize(); err != nil {
		return nil, fmt.Errorf("error discovering MTU size: %v", err)
	}
	if err := state.openConnectionRequest(); err != nil {
		return nil, fmt.Errorf("error receiving open connection reply: %v", err)
	}

	conn := newConn(&wrappedConn{PacketConn: packetConn}, udpConn.RemoteAddr(), state.mtuSize, id)
	go func() {
		// Wait for the connection to be closed...
		<-conn.closeCtx.Done()
		if err := conn.conn.Close(); err != nil {
			// Should not happen.
			panic(err)
		}
	}()
	if err := conn.requestConnection(); err != nil {
		return nil, fmt.Errorf("error requesting connection: %v", err)
	}

	go clientListen(conn, udpConn, dialer.ErrorLog)
	select {
	case <-conn.completingSequence.Done():
		// Clear all read deadlines as we no longer need these.
		_ = udpConn.SetReadDeadline(time.Time{})
		_ = conn.SetReadDeadline(time.Time{})
		return conn, nil
	case <-time.After(time.Second * 10):
		return nil, fmt.Errorf("error establishing a connection: connection timed out")
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
	b := make([]byte, 1492)
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
		if err := rakConn.receive(bytes.NewBuffer(b[:n])); err != nil {
			errorLog.Printf("error handling packet: %v\n", err)
		}
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
	mtuSize int16

	// discoveringMTUSize is the current MTU size 'discovered'. This MTU size decreases the more the open
	// connection request 1 is sent, so that the max packet size can be discovered.
	discoveringMTUSize int16
}

// openConnectionRequest sends open connection request 2 packets continuously until it receives an open
// connection reply 2 packet from the server.
func (state *connState) openConnectionRequest() (e error) {
	ticker := time.NewTicker(time.Second / 2)
	defer ticker.Stop()
	stop := make(chan bool, 1)
	defer func() {
		stop <- true
	}()
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := state.sendOpenConnectionRequest2(); err != nil {
					e = err
					return
				}
			case <-stop:
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
		state.mtuSize = reply.MTUSize
		return
	}
}

// discoverMTUSize starts discovering an MTU size, the maximum packet size we can send, by sending multiple
// open connection request 1 packets to the server with a decreasing MTU size padding.
func (state *connState) discoverMTUSize() (e error) {
	ticker := time.NewTicker(time.Second / 2)
	defer ticker.Stop()
	stop := make(chan bool, 1)
	defer func() {
		stop <- true
	}()
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := state.sendOpenConnectionRequest1(); err != nil {
					e = err
					return
				}
				// Each half second we decrease the MTU size by 40. This means that in 10 seconds, we have an MTU
				// size of 692. This is a little above the actual RakNet minimum, but that should not be an issue.
				state.discoveringMTUSize -= 40
			case <-stop:
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
			if response.MTUSize < 400 || response.MTUSize > 1500 {
				e = fmt.Errorf("invalid MTU size %v received in open connection reply 1", response.MTUSize)
				// Just try again. Some servers for some reason send broken MTU sizes in the first packet.
				continue
			}
			state.mtuSize = response.MTUSize
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
func (state *connState) sendOpenConnectionRequest2() error {
	b := bytes.NewBuffer(nil)
	(&message.OpenConnectionRequest2{ServerAddress: *state.remoteAddr.(*net.UDPAddr), MTUSize: state.mtuSize, ClientGUID: state.id}).Write(b)
	_, err := state.conn.Write(b.Bytes())
	return err
}

// sendOpenConnectionRequest1 sends an open connection request 1 packet to the server. If not successful, an
// error is returned.
func (state *connState) sendOpenConnectionRequest1() error {
	b := bytes.NewBuffer(nil)
	(&message.OpenConnectionRequest1{Protocol: currentProtocol, MTUSize: state.discoveringMTUSize}).Write(b)
	_, err := state.conn.Write(b.Bytes())
	return err
}
