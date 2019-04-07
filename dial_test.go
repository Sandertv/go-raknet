package raknet

import (
	"testing"
)

func TestDial(t *testing.T) {
	data, err := Ping("mco.mineplex.com:19132")
	if err != nil {
		t.Error(err)
	}
	if string(data[:4]) != "MCPE" {
		t.Errorf("invalid pong data received: expected the first bit to be MCPE, but got %v", string(data[:4]))
	}

	conn, err := Dial("mco.mineplex.com:19132")
	if err != nil {
		t.Error(err)
	}
	if conn.mtuSize != 1400 {
		t.Errorf("expected an MTU size of 1400, but got %v", conn.mtuSize)
	}
}
