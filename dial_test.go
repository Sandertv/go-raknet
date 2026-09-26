package raknet_test

import (
	"net"
	"testing"

	"github.com/sandertv/go-raknet"
)

func TestPing(t *testing.T) {
	l := testListener(t)
	const pong = "MCPE;Local test server"
	l.PongData([]byte(pong))
	addr := l.Addr().String()

	data, err := raknet.Ping(addr)
	if err != nil {
		t.Fatalf("error pinging %v: %v", addr, err)
	}
	if string(data) != pong {
		t.Fatalf("ping data should be %q, but got %q", pong, data)
	}
}

func TestPingWithCustomDialer(t *testing.T) {
	l := testListener(t)
	const pong = "MCPE;Local test server"
	l.PongData([]byte(pong))
	addr := l.Addr().String()

	dialer := raknet.Dialer{
		UpstreamDialer: &net.Dialer{
			LocalAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)},
		},
	}

	data, err := dialer.Ping(addr)
	if err != nil {
		t.Fatalf("error pinging %v: %v", addr, err)
	}
	if string(data) != pong {
		t.Fatalf("ping data should be %q, but got %q", pong, data)
	}
}

func TestDial(t *testing.T) {
	l := testListener(t)
	addr := l.Addr().String()

	conn, err := raknet.Dial(addr)
	if err != nil {
		t.Fatalf("error connecting to %v: %v", addr, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("error closing connection: %v", err)
	}
	acceptTestConnection(t, l)
}

func TestDialWithCustomDialer(t *testing.T) {
	l := testListener(t)
	addr := l.Addr().String()

	dialer := raknet.Dialer{
		UpstreamDialer: &net.Dialer{
			LocalAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)},
		},
	}
	conn, err := dialer.Dial(addr)
	if err != nil {
		t.Fatalf("error connecting to %v: %v", addr, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("error closing connection: %v", err)
	}
	acceptTestConnection(t, l)
}
