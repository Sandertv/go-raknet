package internal

import (
	"net"
)

// ConnToPacketConn wraps around a dialed UDP connection. Its only purpose
// is to wrap around WriteTo and make it call Write instead, because WriteTo
// is invalid on a dialed connection.
func ConnToPacketConn(conn net.Conn) net.PacketConn {
	return &wrappedConn{conn}
}

// wrappedConn wraps around a 'pre-connected' UDP connection. Its only purpose
// is to wrap around WriteTo and make it call Write instead.
type wrappedConn struct {
	net.Conn
}

// WriteTo wraps around net.PacketConn to replace functionality of WriteTo with
// Write. It is used to be able to re-use the functionality in raknet.Conn.
func (conn *wrappedConn) WriteTo(b []byte, _ net.Addr) (n int, err error) {
	return conn.Conn.Write(b)
}

func (conn *wrappedConn) ReadFrom([]byte) (int, net.Addr, error) { panic("unused") }
