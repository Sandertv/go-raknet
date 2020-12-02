package raknet_test

import (
	"net"
	"strings"
	"testing"

	"github.com/sandertv/go-raknet"
)

func TestPing(t *testing.T) {
	//noinspection SpellCheckingInspection
	const (
		addr   = "mco.mineplex.com:19132"
		prefix = "MCPE"
	)

	data, err := raknet.Ping(addr)
	if err != nil {
		t.Fatalf("error pinging %v: %v", addr, err)
	}
	str := string(data)
	if !strings.HasPrefix(str, prefix) {
		t.Fatalf("ping data should have prefix %v, but got %v", prefix, str)
	}
}

func TestPingWithCustomDialer(t *testing.T) {
	//noinspection SpellCheckingInspection
	const (
		addr   = "mco.mineplex.com:19132"
		prefix = "MCPE"
	)

	localDialAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:55556")
	if err != nil {
		t.Fatalf("error resolving local dial address: %v", err)
	}

	dialer := raknet.Dialer{
		UpstreamDialer: &net.Dialer{
			LocalAddr: localDialAddr,
		},
	}

	data, err := dialer.Ping(addr)
	if err != nil {
		t.Fatalf("error pinging %v: %v", addr, err)
	}
	str := string(data)
	if !strings.HasPrefix(str, prefix) {
		t.Fatalf("ping data should have prefix %v, but got %v", prefix, str)
	}
}

func TestDial(t *testing.T) {
	//noinspection SpellCheckingInspection
	const (
		addr = "mco.mineplex.com:19132"
	)

	conn, err := raknet.Dial(addr)
	if err != nil {
		t.Fatalf("error connecting to %v: %v", addr, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("error closing connection: %v", err)
	}
}

func TestDialWithCustomDialer(t *testing.T) {
	//noinspection SpellCheckingInspection
	const (
		addr = "mco.mineplex.com:19132"
	)

	localDialAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:55555")
	if err != nil {
		t.Fatalf("error resolving local dial address: %v", err)
	}

	dialer := raknet.Dialer{
		UpstreamDialer: &net.Dialer{
			LocalAddr: localDialAddr,
		},
	}
	conn, err := dialer.Dial(addr)
	if err != nil {
		t.Fatalf("error connecting to %v: %v", addr, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("error closing connection: %v", err)
	}
}
