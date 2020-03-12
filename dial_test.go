package raknet

import (
	"strings"
	"testing"
)

func TestPing(t *testing.T) {
	//noinspection SpellCheckingInspection
	const (
		addr   = "mco.mineplex.com:19132"
		prefix = "MCPE"
	)

	data, err := Ping(addr)
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

	conn, err := Dial(addr)
	if err != nil {
		t.Fatalf("error connecting to %v: %v", addr, err)
	}
	if conn.mtuSize < 1200 || conn.mtuSize > 1500 {
		t.Fatalf("expected MTU size is bigger than 1200 and smaller than 1500, but got %v", conn.mtuSize)
	}
}
