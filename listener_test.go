package raknet_test

import (
	"fmt"
	"github.com/sandertv/go-raknet"
	"testing"
	"time"
)

func TestListen(t *testing.T) {
	l, err := raknet.Listen(":19132")
	if err != nil {
		panic(err)
	}
	go func() {
		_, _ = raknet.Dial("127.0.0.1:19132")
	}()
	c := make(chan error)
	go accept(l, c)

	select {
	case err := <-c:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second * 3):
		t.Errorf("accepting connection took longer than 3 seconds")
	}
}

func TestCloseWithUnacceptedConn(t *testing.T) {
	l, err := raknet.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := raknet.Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Nobody accepts the connection, so the listener holds on to it until it
	// is closed. Closing it must not panic, and Accept must then fail.
	time.Sleep(time.Millisecond * 100)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond * 100)
	if _, err := l.Accept(); err == nil {
		t.Error("accept on a closed listener succeeded")
	}
}

func accept(l *raknet.Listener, c chan error) {
	if _, err := l.Accept(); err != nil {
		c <- fmt.Errorf("error accepting connection: %v", err)
	}
	c <- nil
}
