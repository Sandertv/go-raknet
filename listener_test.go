package raknet

import (
	"testing"
)

func TestListen(t *testing.T) {
	l, err := Listen(":19132")
	if err != nil {
		panic(err)
	}
	go func() {
		if _, err := Dial("127.0.0.1:19132"); err != nil {
			t.Fatalf("error connecting to listener: %v", err)
		}
	}()
	for {
		if _, err := l.Accept(); err != nil {
			t.Fatalf("error accepting connection: %v", err)
			return
		}
		return
	}
}
