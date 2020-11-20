package raknet_test

import (
	"github.com/sandertv/go-raknet"
	"strings"
	"testing"
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
