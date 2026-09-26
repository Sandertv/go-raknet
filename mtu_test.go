package raknet

import (
	"net"
	"testing"
	"time"
)

func TestNextSendMTUProbe(t *testing.T) {
	c := &Conn{mtu: 1492}
	c.sendMTU.Store(1200)
	if g := c.nextSendMTUProbe(); g != 1280 {
		t.Fatalf("from 1200: got %d", g)
	}
	c.sendMTU.Store(1280)
	if g := c.nextSendMTUProbe(); g != 1400 {
		t.Fatalf("from 1280: got %d", g)
	}
	c.sendMTU.Store(1400)
	if g := c.nextSendMTUProbe(); g != 1492 {
		t.Fatalf("from 1400: got %d", g)
	}
	c.sendMTU.Store(1492)
	if g := c.nextSendMTUProbe(); g != 0 {
		t.Fatalf("at max: got %d", g)
	}
	c.sendMTU.Store(1200)
	c.mtu = 1200
	if g := c.nextSendMTUProbe(); g != 0 {
		t.Fatalf("handshake-capped: got %d", g)
	}
}

func TestOnProbeAckRaisesSendMTU(t *testing.T) {
	c := &Conn{mtu: 1492}
	c.sendMTU.Store(1200)
	c.discover.pending = 7
	c.discover.size = 1280
	c.onProbeAck(7)
	if c.MTU() != 1280 {
		t.Fatalf("got %d", c.MTU())
	}
	c.onProbeAck(7)
	if c.MTU() != 1280 {
		t.Fatalf("stale ack changed mtu: %d", c.MTU())
	}
}

func TestOnProbeAckDoesNotExceedNegotiated(t *testing.T) {
	c := &Conn{mtu: 1400}
	c.sendMTU.Store(1200)
	c.discover.pending = 1
	c.discover.size = 1492
	c.onProbeAck(1)
	if c.MTU() != 1200 {
		t.Fatalf("raised past negotiated: %d", c.MTU())
	}
}

func TestInitialSendMTUFuncHint(t *testing.T) {
	l, err := ListenConfig{
		InitialSendMTU: 1200,
		InitialSendMTUFunc: func(net.Addr) uint16 {
			return 1400
		},
	}.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	errc := make(chan error, 1)
	var server *Conn
	go func() {
		c, err := l.Accept()
		if err != nil {
			errc <- err
			return
		}
		server = c.(*Conn)
		errc <- nil
	}()

	client, err := Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("accept timeout")
	}
	defer server.Close()

	if server.MTU() < 1400 {
		t.Fatalf("hint ignored: send MTU %d", server.MTU())
	}
}

func TestSendPathMTUDiscoveryLocalhost(t *testing.T) {
	l, err := ListenConfig{InitialSendMTU: 1200}.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	errc := make(chan error, 1)
	var server *Conn
	go func() {
		c, err := l.Accept()
		if err != nil {
			errc <- err
			return
		}
		server = c.(*Conn)
		errc <- nil
	}()

	client, err := Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("accept timeout")
	}
	defer server.Close()

	if server.MTU() != 1200 {
		t.Fatalf("server send MTU at accept: got %d want 1200", server.MTU())
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if server.MTU() == maxMTUSize {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server send MTU did not reach %d, stuck at %d", maxMTUSize, server.MTU())
}

type dropLargeListener struct{ max int }

func (d dropLargeListener) ListenPacket(network, address string) (net.PacketConn, error) {
	c, err := net.ListenPacket(network, address)
	if err != nil {
		return nil, err
	}
	return dropLarge{PacketConn: c, max: d.max}, nil
}

type dropLarge struct {
	net.PacketConn
	max int
}

func (d dropLarge) WriteTo(p []byte, addr net.Addr) (int, error) {
	if len(p) > d.max {
		return len(p), nil
	}
	return d.PacketConn.WriteTo(p, addr)
}

func TestSendPathMTUDiscoveryStopsWhenLargeDropped(t *testing.T) {
	l, err := ListenConfig{
		InitialSendMTU:         1200,
		UpstreamPacketListener: dropLargeListener{max: 1200 - 28},
	}.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	errc := make(chan error, 1)
	var server *Conn
	go func() {
		c, err := l.Accept()
		if err != nil {
			errc <- err
			return
		}
		server = c.(*Conn)
		errc <- nil
	}()

	client, err := Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("accept timeout")
	}
	defer server.Close()

	time.Sleep(2 * time.Second)
	if server.MTU() != 1200 {
		t.Fatalf("low-MTU path raised send MTU to %d", server.MTU())
	}
}
