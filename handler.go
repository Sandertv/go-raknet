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
	limitsEnabled() bool
	close(conn *Conn)
}

type listenerConnectionHandler struct{ l *Listener }

var (
	errUnexpectedCRA           = errors.New("unexpected CONNECTION_REQUEST_ACCEPTED packet")
	errUnexpectedAdditionalNIC = errors.New("unexpected additional NEW_INCOMING_CONNECTION packet")
)

func (h listenerConnectionHandler) limitsEnabled() bool {
	return true
}

func (h listenerConnectionHandler) close(conn *Conn) {
	h.l.connections.Delete(resolve(conn.raddr))
}

func (h listenerConnectionHandler) handleUnconnected(b []byte, addr net.Addr) error {
	switch b[0] {
	case message.IDUnconnectedPing, message.IDUnconnectedPingOpenConnections:
		return h.handleUnconnectedPing(b[1:], addr)
	case message.IDOpenConnectionRequest1:
		return h.handleOpenConnectionRequest1(b[1:], addr)
	case message.IDOpenConnectionRequest2:
		return h.handleOpenConnectionRequest2(b[1:], addr)
	}
	if b[0]&bitFlagDatagram != 0 {
		// In some cases, the client will keep trying to send datagrams
		// while it has already timed out. In this case, we should not return
		// an error.
		return nil
	}
	return fmt.Errorf("unknown packet received (len=%v): %x", len(b), b)
}

func (h listenerConnectionHandler) handle(conn *Conn, b []byte) (handled bool, err error) {
	switch b[0] {
	case message.IDConnectionRequest:
		return true, h.handleConnectionRequest(conn, b[1:])
	case message.IDConnectionRequestAccepted:
		return true, errUnexpectedCRA
	case message.IDNewIncomingConnection:
		return true, h.handleNewIncomingConnection(conn)
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

// handleUnconnectedPing handles an unconnected ping packet stored in buffer b,
// coming from an address.
func (h listenerConnectionHandler) handleUnconnectedPing(b []byte, addr net.Addr) error {
	pk := &message.UnconnectedPing{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read UNCONNECTED_PING: %w", err)
	}
	data, _ := (&message.UnconnectedPong{ServerGUID: h.l.id, SendTimestamp: pk.SendTimestamp, Data: *h.l.pongData.Load()}).MarshalBinary()
	_, err := h.l.conn.WriteTo(data, addr)
	return err
}

// handleOpenConnectionRequest1 handles an open connection request 1 packet
// stored in buffer b, coming from an address.
func (h listenerConnectionHandler) handleOpenConnectionRequest1(b []byte, addr net.Addr) error {
	pk := &message.OpenConnectionRequest1{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read OPEN_CONNECTION_REQUEST_1: %w", err)
	}
	mtuSize := min(pk.MaximumSizeNotDropped, maxMTUSize)

	if pk.Protocol != protocolVersion {
		data, _ := (&message.IncompatibleProtocolVersion{ServerGUID: h.l.id, ServerProtocol: protocolVersion}).MarshalBinary()
		_, _ = h.l.conn.WriteTo(data, addr)
		return fmt.Errorf("handle OPEN_CONNECTION_REQUEST_1: incompatible protocol version %v (listener protocol = %v)", pk.Protocol, protocolVersion)
	}

	data, _ := (&message.OpenConnectionReply1{ServerGUID: h.l.id, Secure: false, ServerPreferredMTUSize: mtuSize}).MarshalBinary()
	_, err := h.l.conn.WriteTo(data, addr)
	return err
}

// handleOpenConnectionRequest2 handles an open connection request 2 packet
// stored in buffer b, coming from an address.
func (h listenerConnectionHandler) handleOpenConnectionRequest2(b []byte, addr net.Addr) error {
	pk := &message.OpenConnectionRequest2{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read OPEN_CONNECTION_REQUEST_2: %w", err)
	}
	mtuSize := min(pk.ClientPreferredMTUSize, maxMTUSize)

	data, _ := (&message.OpenConnectionReply2{ServerGUID: h.l.id, ClientAddress: resolve(addr), MTUSize: mtuSize}).MarshalBinary()
	if _, err := h.l.conn.WriteTo(data, addr); err != nil {
		return fmt.Errorf("send OPEN_CONNECTION_REPLY_2: %w", err)
	}

	conn := newConn(h.l.conn, addr, mtuSize, h)
	h.l.connections.Store(resolve(addr), conn)

	go func() {
		t := time.NewTimer(time.Second * 10)
		defer t.Stop()
		select {
		case <-conn.connected:
			// Add the connection to the incoming channel so that a caller of
			// Accept() can receive it.
			h.l.incoming <- conn
		case <-h.l.closed:
			_ = conn.Close()
		case <-t.C:
			// It took too long to complete this connection. We closed it and go
			// back to accepting.
			_ = conn.Close()
		}
	}()

	return nil
}

// handleConnectionRequest handles a connection request packet inside of buffer
// b. An error is returned if the packet was invalid.
func (h listenerConnectionHandler) handleConnectionRequest(conn *Conn, b []byte) error {
	pk := &message.ConnectionRequest{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read CONNECTION_REQUEST: %w", err)
	}
	return conn.send(&message.ConnectionRequestAccepted{ClientAddress: resolve(conn.raddr), RequestTimestamp: pk.RequestTimestamp, AcceptedTimestamp: timestamp()})
}

// handleNewIncomingConnection handles an incoming connection packet from the
// client, finalising the Conn.
func (h listenerConnectionHandler) handleNewIncomingConnection(conn *Conn) error {
	select {
	case <-conn.connected:
		return errUnexpectedAdditionalNIC
	default:
		close(conn.connected)
	}
	return nil
}

type dialerConnectionHandler struct{}

var (
	errUnexpectedCR            = errors.New("unexpected CONNECTION_REQUEST packet")
	errUnexpectedAdditionalCRA = errors.New("unexpected additional CONNECTION_REQUEST_ACCEPTED packet")
	errUnexpectedNIC           = errors.New("unexpected NEW_INCOMING_CONNECTION packet")
)

func (h dialerConnectionHandler) close(conn *Conn) {
	_ = conn.conn.Close()
}

func (h dialerConnectionHandler) limitsEnabled() bool {
	return false
}

func (h dialerConnectionHandler) handle(conn *Conn, b []byte) (handled bool, err error) {
	switch b[0] {
	case message.IDConnectionRequest:
		return true, errUnexpectedCR
	case message.IDConnectionRequestAccepted:
		return true, h.handleConnectionRequestAccepted(conn, b[1:])
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

// handleConnectionRequestAccepted handles a serialised connection request
// accepted packet in b, and returns an error if not successful.
func (h dialerConnectionHandler) handleConnectionRequestAccepted(conn *Conn, b []byte) error {
	pk := &message.ConnectionRequestAccepted{}
	if err := pk.UnmarshalBinary(b); err != nil {
		return fmt.Errorf("read CONNECTION_REQUEST_ACCEPTED: %w", err)
	}
	select {
	case <-conn.connected:
		return errUnexpectedAdditionalCRA
	default:
		// Make sure to send NewIncomingConnection before closing conn.connected.
		err := conn.send(&message.NewIncomingConnection{ServerAddress: resolve(conn.raddr), RequestTimestamp: pk.AcceptedTimestamp, AcceptedTimestamp: timestamp()})
		close(conn.connected)
		return err
	}
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
