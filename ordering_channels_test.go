package raknet

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestPacketReadWriteOrderChannel(t *testing.T) {
	in := &packet{
		reliability:  reliabilityReliableOrdered,
		messageIndex: 33,
		orderIndex:   19,
		orderChannel: 7,
		content:      []byte{0x10, 0x20, 0x30},
	}
	var buf bytes.Buffer
	in.write(&buf)

	var out packet
	n, err := out.read(buf.Bytes())
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	if n != buf.Len() {
		t.Fatalf("read bytes mismatch: got %v, expected %v", n, buf.Len())
	}
	if out.reliability != in.reliability {
		t.Fatalf("reliability mismatch: got %v, expected %v", out.reliability, in.reliability)
	}
	if out.messageIndex != in.messageIndex {
		t.Fatalf("message index mismatch: got %v, expected %v", out.messageIndex, in.messageIndex)
	}
	if out.orderIndex != in.orderIndex {
		t.Fatalf("order index mismatch: got %v, expected %v", out.orderIndex, in.orderIndex)
	}
	if out.orderChannel != in.orderChannel {
		t.Fatalf("order channel mismatch: got %v, expected %v", out.orderChannel, in.orderChannel)
	}
	if !bytes.Equal(out.content, in.content) {
		t.Fatalf("content mismatch: got %x, expected %x", out.content, in.content)
	}
}

func TestPacketChannelQueuePreservesChannelStateAfterDrain(t *testing.T) {
	queue := newPacketChannelQueue()

	if !queue.put(3, 0, []byte("x0")) {
		t.Fatal("put channel 3 index 0 failed")
	}
	packets := queue.fetch(3)
	if len(packets) != 1 || string(packets[0]) != "x0" {
		t.Fatalf("expected only x0 for channel 3, got %q", packets)
	}

	// Duplicate of an already consumed packet must still be rejected.
	if queue.put(3, 0, []byte("x0-dup")) {
		t.Fatal("expected duplicate after drain to be rejected")
	}

	if !queue.put(3, 1, []byte("x1")) {
		t.Fatal("put channel 3 index 1 failed")
	}
	packets = queue.fetch(3)
	if len(packets) != 1 || string(packets[0]) != "x1" {
		t.Fatalf("expected only x1 for channel 3, got %q", packets)
	}
}

type recordingPacketConn struct {
	writes [][]byte
}

func (conn *recordingPacketConn) ReadFrom(_ []byte) (n int, addr net.Addr, err error) {
	return 0, nil, io.EOF
}

func (conn *recordingPacketConn) WriteTo(p []byte, _ net.Addr) (n int, err error) {
	b := make([]byte, len(p))
	copy(b, p)
	conn.writes = append(conn.writes, b)
	return len(p), nil
}

func (conn *recordingPacketConn) Close() error                       { return nil }
func (conn *recordingPacketConn) LocalAddr() net.Addr                { return &net.UDPAddr{} }
func (conn *recordingPacketConn) SetDeadline(_ time.Time) error      { return nil }
func (conn *recordingPacketConn) SetReadDeadline(_ time.Time) error  { return nil }
func (conn *recordingPacketConn) SetWriteDeadline(_ time.Time) error { return nil }

func TestConnWriteUsesPerChannelOrderIndex(t *testing.T) {
	wire := &recordingPacketConn{}
	conn := &Conn{
		conn:           wire,
		raddr:          &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 19132},
		mtu:            1400,
		buf:            bytes.NewBuffer(make([]byte, 0, 1400-28)),
		retransmission: newRecoveryQueue(),
	}

	if _, err := conn.write([]byte{0x01}, reliabilityReliableOrdered, 2); err != nil {
		t.Fatalf("write packet 1: %v", err)
	}
	if _, err := conn.write([]byte{0x02}, reliabilityReliableOrdered, 2); err != nil {
		t.Fatalf("write packet 2: %v", err)
	}
	if _, err := conn.write([]byte{0x03}, reliabilityReliableOrdered, 5); err != nil {
		t.Fatalf("write packet 3: %v", err)
	}

	if len(wire.writes) != 3 {
		t.Fatalf("expected 3 datagrams, got %v", len(wire.writes))
	}

	first := decodeDatagramPacket(t, wire.writes[0])
	second := decodeDatagramPacket(t, wire.writes[1])
	third := decodeDatagramPacket(t, wire.writes[2])

	if first.orderChannel != 2 || first.orderIndex != 0 {
		t.Fatalf("unexpected first packet order metadata: channel=%v index=%v", first.orderChannel, first.orderIndex)
	}
	if second.orderChannel != 2 || second.orderIndex != 1 {
		t.Fatalf("unexpected second packet order metadata: channel=%v index=%v", second.orderChannel, second.orderIndex)
	}
	if third.orderChannel != 5 || third.orderIndex != 0 {
		t.Fatalf("unexpected third packet order metadata: channel=%v index=%v", third.orderChannel, third.orderIndex)
	}
}

func decodeDatagramPacket(t *testing.T, b []byte) packet {
	t.Helper()

	if len(b) < 4 {
		t.Fatalf("invalid datagram length: %v", len(b))
	}
	if b[0]&bitFlagDatagram == 0 {
		t.Fatalf("missing datagram bitflag: %x", b[0])
	}

	var pk packet
	n, err := pk.read(b[4:])
	if err != nil {
		t.Fatalf("decode packet: %v", err)
	}
	if n != len(b)-4 {
		t.Fatalf("decoded packet bytes mismatch: got %v, expected %v", n, len(b)-4)
	}
	return pk
}
