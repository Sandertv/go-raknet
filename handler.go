package raknet

import (
	"errors"
	"fmt"
	"github.com/sandertv/go-raknet/internal/message"
	"net"
	"time"
)

type connectionHandler interface {
	handle(conn *Conn, b []byte) (handled bool, err error)
	packetLimits() bool
}

type listenerConnectionHandler struct{}

var (
	errUnexpectedCRA           = errors.New("unexpected CONNECTION_REQUEST_ACCEPTED packet")
	errUnexpectedAdditionalNIC = errors.New("unexpected additional NEW_INCOMING_CONNECTION packet")
)

func (h *listenerConnectionHandler) handleUnconnected(l *Listener, b []byte, addr net.Addr) (handled bool, err error) {
	switch b[0] {
	case message.IDUnconnectedPing, message.IDUnconnectedPingOpenConnections:
		return true, handleUnconnectedPing(l, b[1:], addr)
	case message.IDOpenConnectionRequest1:
		return true, handleOpenConnectionRequest1(l, b[1:], addr)
	case message.IDOpenConnectionRequest2:
		return true, handleOpenConnectionRequest2(l, b[1:], addr)
	}
	// In some cases, the client will keep trying to send datagrams
	// while it has already timed out. In this case, we should not print
	// an error.
	if b[0]&bitFlagDatagram != 0 {
		return false, nil
	}
	return true, fmt.Errorf("unknown packet received (len=%v): %x", len(b), b)
}

// handleUnconnectedPing handles an unconnected ping packet stored in buffer b,
// coming from an address.
func handleUnconnectedPing(listener *Listener, b []byte, addr net.Addr) error {
	pk := &message.UnconnectedPing{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read UNCONNECTED_PING: %w", err)
	}
	data, _ := (&message.UnconnectedPong{ServerGUID: listener.id, SendTimestamp: pk.SendTimestamp, Data: *listener.pongData.Load()}).MarshalBinary()
	_, err := listener.conn.WriteTo(data, addr)
	return err
}

// handleOpenConnectionRequest1 handles an open connection request 1 packet
// stored in buffer b, coming from an address.
func handleOpenConnectionRequest1(listener *Listener, b []byte, addr net.Addr) error {
	pk := &message.OpenConnectionRequest1{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read OPEN_CONNECTION_REQUEST_1: %w", err)
	}
	mtuSize := min(pk.MaximumSizeNotDropped, maxMTUSize)

	if pk.Protocol != protocolVersion {
		data, _ := (&message.IncompatibleProtocolVersion{ServerGUID: listener.id, ServerProtocol: protocolVersion}).MarshalBinary()
		_, _ = listener.conn.WriteTo(data, addr)
		return fmt.Errorf("handle OPEN_CONNECTION_REQUEST_1: incompatible protocol version %v (listener protocol = %v)", pk.Protocol, protocolVersion)
	}

	data, _ := (&message.OpenConnectionReply1{ServerGUID: listener.id, Secure: false, ServerPreferredMTUSize: mtuSize}).MarshalBinary()
	_, err := listener.conn.WriteTo(data, addr)
	return err
}

// handleOpenConnectionRequest2 handles an open connection request 2 packet
// stored in buffer b, coming from an address.
func handleOpenConnectionRequest2(listener *Listener, b []byte, addr net.Addr) error {
	pk := &message.OpenConnectionRequest2{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read OPEN_CONNECTION_REQUEST_2: %w", err)
	}
	mtuSize := min(pk.ClientPreferredMTUSize, maxMTUSize)

	data, _ := (&message.OpenConnectionReply2{ServerGUID: listener.id, ClientAddress: resolve(addr), MTUSize: mtuSize}).MarshalBinary()
	if _, err := listener.conn.WriteTo(data, addr); err != nil {
		return fmt.Errorf("send OPEN_CONNECTION_REPLY_2: %w", err)
	}

	conn := newListenerConn(listener.conn, addr, pk.ClientPreferredMTUSize)
	conn.close = func() {
		// Make sure to remove the connection from the Listener once the Conn is
		// closed.
		listener.connections.Delete(resolve(addr))
	}
	listener.connections.Store(resolve(addr), conn)

	go func() {
		t := time.NewTimer(time.Second * 10)
		defer t.Stop()
		select {
		case <-conn.connected:
			// Add the connection to the incoming channel so that a caller of
			// Accept() can receive it.
			listener.incoming <- conn
		case <-listener.closed:
			_ = conn.Close()
		case <-t.C:
			// It took too long to complete this connection. We closed it and go
			// back to accepting.
			_ = conn.Close()
		}
	}()

	return nil
}

func (h *listenerConnectionHandler) handle(conn *Conn, b []byte) (handled bool, err error) {
	switch b[0] {
	case message.IDConnectionRequest:
		return true, handleConnectionRequest(conn, b[1:])
	case message.IDConnectionRequestAccepted:
		return true, errUnexpectedCRA
	case message.IDNewIncomingConnection:
		return true, handleNewIncomingConnection(conn)
	case message.IDConnectedPing:
		return true, handleConnectedPing(conn, b[1:])
	case message.IDConnectedPong:
		return true, handleConnectedPong(b[1:])
	case message.IDDisconnectNotification:
		conn.closeImmediately()
		return true, nil
	case message.IDDetectLostConnections:
		// Let the other end know the connection is still alive.
		return true, conn.send(&message.ConnectedPing{ClientTimestamp: timestamp()})
	default:
		return false, nil
	}
}

func (h *listenerConnectionHandler) packetLimits() bool {
	return true
}

type dialerConnectionHandler struct{}

var (
	errUnexpectedCR  = errors.New("unexpected CONNECTION_REQUEST packet")
	errUnexpectedNIC = errors.New("unexpected NEW_INCOMING_CONNECTION packet")
)

func (h *dialerConnectionHandler) handle(conn *Conn, b []byte) (handled bool, err error) {
	switch b[0] {
	case message.IDConnectionRequest:
		return true, errUnexpectedCR
	case message.IDConnectionRequestAccepted:
		return true, handleConnectionRequestAccepted(conn, b[1:])
	case message.IDNewIncomingConnection:
		return true, errUnexpectedNIC
	case message.IDConnectedPing:
		return true, handleConnectedPing(conn, b[1:])
	case message.IDConnectedPong:
		return true, handleConnectedPong(b[1:])
	case message.IDDisconnectNotification:
		conn.closeImmediately()
		return true, nil
	case message.IDDetectLostConnections:
		// Let the other end know the connection is still alive.
		return true, conn.send(&message.ConnectedPing{ClientTimestamp: timestamp()})
	default:
		return false, nil
	}
}

func (h *dialerConnectionHandler) packetLimits() bool {
	return false
}

// handleConnectedPing handles a connected ping packet inside of buffer b. An
// error is returned if the packet was invalid.
func handleConnectedPing(conn *Conn, b []byte) error {
	pk := message.ConnectedPing{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read CONNECTED_PING: %w", err)
	}

	// Respond with a connected pong that has the ping timestamp found in the
	// connected ping, and our own timestamp for the pong timestamp.
	return conn.send(&message.ConnectedPong{ClientTimestamp: pk.ClientTimestamp, ServerTimestamp: timestamp()})
}

// handleConnectedPong handles a connected pong packet inside of buffer b. An
// error is returned if the packet was invalid.
func handleConnectedPong(b []byte) error {
	pk := &message.ConnectedPong{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read CONNECTED_PONG: %w", err)
	}
	if pk.ClientTimestamp > timestamp() {
		return fmt.Errorf("handle CONNECTED_PONG: timestamp is in the future")
	}
	// We don't actually use the ConnectedPong to measure rtt. It is too
	// unreliable and doesn't give a good idea of the connection quality.
	return nil
}

// handleConnectionRequest handles a connection request packet inside of buffer
// b. An error is returned if the packet was invalid.
func handleConnectionRequest(conn *Conn, b []byte) error {
	pk := &message.ConnectionRequest{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read CONNECTION_REQUEST: %w", err)
	}
	return conn.send(&message.ConnectionRequestAccepted{ClientAddress: resolve(conn.raddr), RequestTimestamp: pk.RequestTimestamp, AcceptedTimestamp: timestamp()})
}

// handleConnectionRequestAccepted handles a serialised connection request
// accepted packet in b, and returns an error if not successful.
func handleConnectionRequestAccepted(conn *Conn, b []byte) error {
	pk := &message.ConnectionRequestAccepted{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read CONNECTION_REQUEST_ACCEPTED: %w", err)
	}

	err := conn.send(&message.NewIncomingConnection{ServerAddress: resolve(conn.raddr), RequestTimestamp: pk.RequestTimestamp, AcceptedTimestamp: pk.AcceptedTimestamp, SystemAddresses: pk.SystemAddresses})

	select {
	case <-conn.connected:
	default:
		close(conn.connected)
	}
	return err
}

// handleNewIncomingConnection handles an incoming connection packet from the
// client, finalising the Conn.
func handleNewIncomingConnection(conn *Conn) error {
	select {
	case <-conn.connected:
		return errUnexpectedAdditionalNIC
	default:
		close(conn.connected)
	}
	return nil
}
