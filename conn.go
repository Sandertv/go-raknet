package raknet

import (
	"bytes"
	"context"
	"encoding"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sandertv/go-raknet/internal"
	"github.com/sandertv/go-raknet/internal/message"
)

const (
	// protocolVersion is the current RakNet protocol version. This is Minecraft
	// specific.
	protocolVersion byte = 11

	minMTUSize    = 400
	maxMTUSize    = 1492
	maxWindowSize = 2048

	// Bound queued application data so a stalled path cannot grow memory forever.
	// Write waits at the limit; the reserve lets control traffic continue.
	maxSendQueueBytes = 16 << 20
	sendQueueReserve  = 64 << 10

	// ackDelay is how long datagram acknowledgements are batched before being
	// sent. It matches the SYN interval the client holds its own ACKs for, and
	// bounds how far the peer's round trip estimate can be inflated by us.
	ackDelay = time.Millisecond * 10

	// resendBufferSize caps reliable datagrams awaiting acknowledgement. The
	// client indexes a fixed 512 slot resend buffer and refuses to send new
	// reliable data while the slot for the next message number is taken, which
	// bounds a burst even when the congestion window is far larger.
	resendBufferSize = 512
)

// queuedDatagram is a datagram waiting to be sent, with its wire size and the
// bytes it counts in flight.
type queuedDatagram struct {
	pk            *packet
	wireSize      uint32
	inFlightBytes uint32
}

// Conn represents a connection to a specific client. It is not a real
// connection, as UDP is connectionless, but rather a connection emulated using
// RakNet. Methods may be called on Conn from multiple goroutines
// simultaneously.
type Conn struct {
	// rtt is the last measured round-trip time between both ends of the
	// connection. The rtt is measured in nanoseconds.
	rtt atomic.Int64

	// systemStart is the timestamp indicating when the remote system was started,
	// collected via timestamps.
	systemStart time.Time

	closing atomic.Int64

	ctx        context.Context
	cancelFunc context.CancelFunc

	conn    net.PacketConn
	raddr   net.Addr
	handler connectionHandler

	once      sync.Once
	connected chan struct{}

	mu  sync.Mutex
	buf *bytes.Buffer

	ackBuf, nackBuf *bytes.Buffer

	pk *packet

	seq, orderIndex, messageIndex uint24
	splitID                       uint32

	// mtu is the MTU size of the connection. Packets longer than this size
	// must be split into fragments for them to arrive at the client without
	// losing bytes.
	mtu uint16

	// splits is a map of slices indexed by split IDs. The length of each of the
	// slices is equal to the split count, and packets are positioned in that
	// slice indexed by the split index.
	splits map[uint16][][]byte

	// win is an ordered queue used to track which datagrams were received and
	// which datagrams were missing, so that we can send NACKs to request
	// missing datagrams.
	win *datagramWindow

	ackMu sync.Mutex
	// ackSlice is a slice containing sequence numbers of datagrams that were
	// received over the last second. When ticked, all of these packets are sent
	// in an ACK and the slice is cleared.
	ackSlice []uint24
	// oldestUnsentAck is when the first sequence number still in ackSlice was
	// received. ACKs are held back until ackDelay has passed since then.
	oldestUnsentAck time.Time
	// ackedAny records whether any ACK has been received. Until one has, ACKs
	// are flushed without delay, as the peer's retransmission timer is unknown.
	ackedAny atomic.Bool
	// ackTimer wakes the send loop once a batch has been held for ackDelay, so
	// a batch that no further traffic follows is not left until the next tick.
	ackTimer *time.Timer

	// packetQueue is an ordered queue containing packets indexed by their order
	// index.
	packetQueue *packetQueue
	// packets is a channel containing content of packets that were fully
	// processed. Calling Conn.Read() consumes a value from this channel.
	packets *internal.ElasticChan[[]byte]

	// retransmission is a queue filled with packets that were sent with a given
	// datagram sequence number.
	retransmission *resendMap

	congestion congestionWindow
	// controlQueue is drained before application data.
	controlQueue   []queuedDatagram
	sendQueue      []queuedDatagram
	sendQueueBytes uint32
	// sendQueueFreed is closed and replaced when the send queue frees space, so
	// that every writer waiting on it wakes to re-check its own requirement.
	sendQueueFreed chan struct{}
	sendBudget     uint32 // bytes still sendable this tick, refreshed each update
	continuousSend bool   // data still queued last update; the window only grows then
	wireContinuous bool   // per-datagram continuous-send flag on the wire
	// sendSignal wakes the connection's own goroutine, which owns every send.
	// Receiving goroutines only mutate state and signal, so a window drain
	// never blocks the shared read loop of a Listener.
	sendSignal chan struct{}

	lastActivity atomic.Pointer[time.Time]
}

// newConn constructs a new connection specifically dedicated to the address
// passed.
func newConn(conn net.PacketConn, raddr net.Addr, mtu uint16, h connectionHandler) *Conn {
	mtu = clampMTU(mtu, minMTUSize)
	c := &Conn{
		raddr:          raddr,
		conn:           conn,
		mtu:            mtu,
		handler:        h,
		pk:             new(packet),
		connected:      make(chan struct{}),
		packets:        internal.Chan[[]byte](4, 4096),
		splits:         make(map[uint16][][]byte),
		win:            newDatagramWindow(),
		packetQueue:    newPacketQueue(),
		retransmission: newRecoveryQueue(),
		congestion:     newCongestionWindow(mtu - 28),
		sendQueueFreed: make(chan struct{}),
		sendSignal:     make(chan struct{}, 1),
		sendBudget:     uint32(mtu - 28),
		buf:            bytes.NewBuffer(make([]byte, 0, mtu-28)), // - headers.
		ackBuf:         bytes.NewBuffer(make([]byte, 0, 128)),
		nackBuf:        bytes.NewBuffer(make([]byte, 0, 64)),
	}
	c.ctx, c.cancelFunc = context.WithCancel(context.Background())
	t := time.Now()
	c.lastActivity.Store(&t)
	go c.startTicking()
	return c
}

// clampMTU bounds mtu to the supported range.
func clampMTU(mtu, minMTU uint16) uint16 {
	if mtu == 0 || mtu > maxMTUSize {
		return maxMTUSize
	}
	return max(mtu, minMTU)
}

// effectiveMTU returns the mtu size without the space allocated for IP and
// UDP headers (28 bytes).
func (conn *Conn) effectiveMTU() uint16 {
	return conn.mtu - 28
}

// startTicking makes the connection start ticking, sending ACKs and pings to
// the other end where necessary and checking if the connection should be timed
// out.
func (conn *Conn) startTicking() {
	var (
		interval = time.Second / 10
		ticker   = time.NewTicker(interval)
		i        int64
		acksLeft int
	)
	defer ticker.Stop()
	for {
		select {
		case <-conn.sendSignal:
			// Protocol activity opened the window, queued data or made a
			// retransmission due. The client runs the same update on a 10ms
			// timer that network activity signals early.
			conn.flushACKs()
			conn.update(time.Now())
		case t := <-ticker.C:
			i++
			conn.flushACKs()
			conn.update(t)
			if unix := conn.closing.Load(); unix != 0 {
				before := acksLeft
				conn.mu.Lock()
				acksLeft = len(conn.retransmission.unacknowledged)
				conn.mu.Unlock()

				if before != 0 && acksLeft == 0 {
					conn.closeImmediately()
				}
				since := t.Sub(time.Unix(unix, 0))
				if (acksLeft == 0 && since > time.Second) || since > time.Second*5 {
					conn.closeImmediately()
				}
				continue
			}
			if i%5 == 0 {
				_ = conn.send(&message.ConnectedPing{PingTime: timestamp()})

				conn.mu.Lock()
				timedOut := t.Sub(*conn.lastActivity.Load()) > time.Second*5+conn.retransmission.rtt()*2
				conn.mu.Unlock()
				if timedOut {
					// Close sends a disconnect through Write, which takes conn.mu.
					// Do not call it while holding the same non-reentrant mutex.
					_ = conn.Close()
				}
			}
		case <-conn.ctx.Done():
			return
		}
	}
}

// flushACKs flushes pending datagram acknowledgements once they have been held
// for ackDelay, batching the sequence numbers received in that window.
func (conn *Conn) flushACKs() {
	conn.ackMu.Lock()
	defer conn.ackMu.Unlock()

	if len(conn.ackSlice) == 0 || !conn.ackDue(time.Now()) {
		return
	}
	// Write an ACK packet to the connection containing all datagram sequence
	// numbers that we received since the last flush.
	if err := conn.sendACK(conn.ackSlice...); err != nil {
		return
	}
	conn.ackSlice = conn.ackSlice[:0]
	conn.oldestUnsentAck = time.Time{}
}

// ackDue reports whether pending ACKs have been held long enough. It must be
// called with ackMu held.
func (conn *Conn) ackDue(now time.Time) bool {
	if !conn.ackedAny.Load() {
		// The peer's retransmission timer is still unknown, so acknowledge
		// without delay rather than risk a spurious resend.
		return true
	}
	return !now.Before(conn.oldestUnsentAck.Add(ackDelay))
}

// update runs one send cycle: it recomputes the transmission budget from the
// congestion window, retransmits records whose timer expired and drains as much
// of the send queue as the budget allows. Only the connection's own goroutine
// may call it.
func (conn *Conn) update(now time.Time) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	conn.congestion.continuous = conn.continuousSend
	conn.wireContinuous = conn.continuousSend
	conn.sendBudget = conn.congestion.transmissionBandwidth()

	rtt := conn.retransmission.rtt()
	conn.rtt.Store(int64(rtt))

	if conn.retransmission.due(now) {
		conn.resendExpired(now)
	}
	_ = conn.drainSendQueue()
	conn.continuousSend = len(conn.controlQueue) != 0 || len(conn.sendQueue) != 0
}

// resendExpired retransmits every record whose send timer expired, oldest
// first, and recomputes the earliest remaining timer.
func (conn *Conn) resendExpired(now time.Time) {
	var resend []uint24
	deadline := time.Time{}
	for seq, record := range conn.retransmission.unacknowledged {
		if !now.Before(record.nextSend) {
			resend = append(resend, seq)
		} else if deadline.IsZero() || record.nextSend.Before(deadline) {
			deadline = record.nextSend
		}
	}
	conn.retransmission.deadline = deadline
	slices.SortFunc(resend, func(a, b uint24) int {
		return conn.retransmission.unacknowledged[a].nextSend.Compare(
			conn.retransmission.unacknowledged[b].nextSend,
		)
	})
	_ = conn.resend(resend)
}

// Write writes a buffer b over the RakNet connection. The amount of bytes
// written n is always equal to the length of the bytes written if writing was
// successful. If not, an error is returned and n is 0. Write may be called
// simultaneously from multiple goroutines, but will write one by one.
func (conn *Conn) Write(b []byte) (n int, err error) {
	return conn.writeWithReliability(b, reliabilityReliableOrdered)
}

// writeWithReliability writes a buffer b over the RakNet connection using the
// reliability passed. The amount of bytes written n is always equal to the
// length of the bytes written if writing was successful. If not, an error is
// returned and n is 0. writeWithReliability may be called simultaneously from
// multiple goroutines, but will write one by one. Unlike Write, it allows
// specifying the reliability.
func (conn *Conn) writeWithReliability(b []byte, rel reliability) (n int, err error) {
	select {
	case <-conn.ctx.Done():
		return 0, conn.error(net.ErrClosed, "write")
	default:
		if len(b) == 0 {
			return 0, nil
		}
		required := conn.queuedSize(b, rel)
		if required > maxSendQueueBytes-sendQueueReserve {
			return 0, conn.error(fmt.Errorf("packet requires %d bytes in the send queue", required), "write")
		}
		for {
			conn.mu.Lock()
			select {
			case <-conn.ctx.Done():
				conn.mu.Unlock()
				return 0, conn.error(net.ErrClosed, "write")
			default:
			}
			if conn.sendQueueBytes+required <= maxSendQueueBytes-sendQueueReserve {
				n, err = conn.write(b, rel, false)
				conn.mu.Unlock()
				if err != nil {
					n = 0
				}
				return n, conn.error(err, "write")
			}
			// Captured under the lock that guards the check above, so a drain
			// between here and the wait closes this channel rather than
			// signalling into one nobody is listening on yet.
			freed := conn.sendQueueFreed
			conn.mu.Unlock()

			select {
			case <-freed:
			case <-conn.ctx.Done():
				return 0, conn.error(net.ErrClosed, "write")
			}
		}
	}
}

// queuedSize returns the wire bytes b occupies in the send queue, headers and
// fragmentation included.
func (conn *Conn) queuedSize(b []byte, rel reliability) uint32 {
	count := splitCount(len(b), conn.effectiveMTU())
	// Fragment payloads sum to len(b), and every fragment costs the same header.
	return uint32(len(b) + count*(encapsulatedPacketSize(0, rel, count > 1)+4))
}

// write writes a buffer b over the RakNet connection. The amount of bytes
// written n is always equal to the length of the bytes written if the write
// was successful. If not, an error is returned and n is 0. Write may be called
// simultaneously from multiple goroutines, but will write one by one. Unlike
// Write, write will not lock.
func (conn *Conn) write(b []byte, rel reliability, control bool) (n int, err error) {
	fragments := split(b, conn.effectiveMTU())
	var orderIndex uint24
	if rel.sequencedOrOrdered() {
		orderIndex = conn.orderIndex.Inc()
	}

	splitID := uint16(conn.splitID)
	if len(fragments) > 1 {
		conn.splitID++
	}
	for splitIndex, content := range fragments {
		pk := packetPool.Get().(*packet)
		if cap(pk.content) < len(content) {
			pk.content = make([]byte, len(content))
		}
		// We set the actual slice size to the same size as the content. It
		// might be bigger than the previous size, in which case it will grow,
		// which is fine as the underlying array will always be big enough.
		pk.content = pk.content[:len(content)]
		copy(pk.content, content)

		pk.orderIndex = orderIndex
		pk.reliability = rel
		if rel.reliable() {
			pk.messageIndex = conn.messageIndex.Inc()
		}
		if pk.split = len(fragments) > 1; pk.split {
			// If there were more than one fragment, the pk was split, so we
			// need to make sure we set the appropriate fields.
			pk.splitCount = uint32(len(fragments))
			pk.splitIndex = uint32(splitIndex)
			pk.splitID = splitID
		}
		inFlightBytes := uint32(encapsulatedPacketSize(len(content), rel, pk.split))
		wireSize := inFlightBytes + 4
		datagram := queuedDatagram{pk: pk, wireSize: wireSize, inFlightBytes: inFlightBytes}
		if control {
			conn.controlQueue = append(conn.controlQueue, datagram)
		} else {
			conn.sendQueue = append(conn.sendQueue, datagram)
		}
		conn.sendQueueBytes += wireSize
		n += len(content)
	}
	conn.signalSend()
	return n, nil
}

// Read reads from the connection into the byte slice passed. If successful,
// the amount of bytes read n is returned, and the error returned will be nil.
// Read blocks until a packet is received over the connection, or until the
// session is closed or the read times out, in which case an error is returned.
func (conn *Conn) Read(b []byte) (n int, err error) {
	pk, ok := conn.packets.Recv(conn.ctx)
	if !ok {
		return 0, conn.error(net.ErrClosed, "read")
	} else if len(b) < len(pk) {
		return 0, conn.error(ErrBufferTooSmall, "read")
	}
	return copy(b, pk), err
}

// ReadPacket attempts to read the next packet as a byte slice. ReadPacket
// blocks until a packet is received over the connection, or until the session
// is closed or the read times out, in which case an error is returned.
func (conn *Conn) ReadPacket() (b []byte, err error) {
	pk, ok := conn.packets.Recv(conn.ctx)
	if !ok {
		return nil, conn.error(net.ErrClosed, "read")
	}
	return pk, err
}

// Close closes the connection. All blocking Read or Write actions are
// cancelled and will return an error, as soon as the closing of the connection
// is acknowledged by the client.
func (conn *Conn) Close() error {
	conn.closing.CompareAndSwap(0, time.Now().Unix())
	return nil
}

// Context returns the connection's context. The context is canceled when
// the connection is closed, allowing for cancellation of operations
// that are tied to the lifecycle of the connection.
func (conn *Conn) Context() context.Context {
	return conn.ctx
}

// closeImmediately sends a Disconnect notification to the other end of the
// connection and closes the underlying UDP connection immediately.
func (conn *Conn) closeImmediately() {
	conn.once.Do(func() {
		conn.mu.Lock()
		// Queue the disconnect and flush it before the handler closes a
		// dialer's socket, since sends otherwise belong to the send loop, which
		// is about to stop. Nothing outstanding will be acknowledged now, so
		// release it first: that opens the resend buffer for the flush.
		conn.releaseUnacknowledged()
		_, _ = conn.write([]byte{message.IDDisconnectNotification}, reliabilityReliableOrdered, true)
		conn.sendBudget = max(conn.sendBudget, uint32(conn.effectiveMTU()))
		_ = conn.drainSendQueue()
		conn.mu.Unlock()

		conn.handler.close(conn)
		conn.cancelFunc()

		conn.mu.Lock()
		defer conn.mu.Unlock()
		// The flush re-added whatever it sent.
		conn.releaseUnacknowledged()
		for _, datagram := range conn.sendQueue {
			datagram.pk.content = datagram.pk.content[:0]
			packetPool.Put(datagram.pk)
		}
		for _, datagram := range conn.controlQueue {
			datagram.pk.content = datagram.pk.content[:0]
			packetPool.Put(datagram.pk)
		}
		conn.controlQueue = nil
		conn.sendQueue = nil
		conn.sendQueueBytes = 0
		conn.signalSendQueueFreed()
	})
}

// releaseUnacknowledged returns every packet awaiting acknowledgement to the
// pool and forgets it. Only for a connection that is closing.
func (conn *Conn) releaseUnacknowledged() {
	for _, record := range conn.retransmission.unacknowledged {
		record.pk.content = record.pk.content[:0]
		packetPool.Put(record.pk)
	}
	clear(conn.retransmission.unacknowledged)
}

// RemoteAddr returns the remote address of the connection, meaning the address
// this connection leads to.
func (conn *Conn) RemoteAddr() net.Addr {
	return conn.raddr
}

// LocalAddr returns the local address of the connection, which is always the
// same as the listener's.
func (conn *Conn) LocalAddr() net.Addr {
	return conn.conn.LocalAddr()
}

// SetReadDeadline is unimplemented. It always returns ErrNotSupported.
func (conn *Conn) SetReadDeadline(time.Time) error { return ErrNotSupported }

// SetWriteDeadline is unimplemented. It always returns ErrNotSupported.
func (conn *Conn) SetWriteDeadline(time.Time) error { return ErrNotSupported }

// SetDeadline is unimplemented. It always returns ErrNotSupported.
func (conn *Conn) SetDeadline(time.Time) error { return ErrNotSupported }

// Latency returns a rolling average of rtt between the sending and the
// receiving end of the connection. The rtt returned is updated continuously
// and is half the average round trip time (RTT).
func (conn *Conn) Latency() time.Duration {
	return time.Duration(conn.rtt.Load() / 2)
}

// send encodes an encoding.BinaryMarshaler and writes it to the Conn.
func (conn *Conn) send(pk encoding.BinaryMarshaler) error {
	b, _ := pk.MarshalBinary()
	return conn.writeControl(b, reliabilityReliableOrdered)
}

// sendUnreliable encodes an encoding.BinaryMarshaler and writes it to the Conn using
// unreliable reliability.
func (conn *Conn) sendUnreliable(pk encoding.BinaryMarshaler) error {
	b, _ := pk.MarshalBinary()
	return conn.writeControl(b, reliabilityUnreliable)
}

// writeControl queues an internal packet (ping, disconnect) ahead of application
// data. It fails rather than blocks when the queue is full.
func (conn *Conn) writeControl(b []byte, rel reliability) error {
	required := conn.queuedSize(b, rel)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.sendQueueBytes+required > maxSendQueueBytes {
		return conn.error(errors.New("send queue is full"), "write")
	}
	_, err := conn.write(b, rel, true)
	return conn.error(err, "write")
}

// packetPool is used to pool packets that encapsulate their content.
var packetPool = sync.Pool{New: func() any { return &packet{reliability: reliabilityReliableOrdered} }}

// receive receives a packet from the connection, handling it as appropriate.
// If not successful, an error is returned.
func (conn *Conn) receive(b []byte) error {
	t := time.Now()
	conn.lastActivity.Store(&t)

	switch {
	case b[0]&bitFlagACK != 0:
		return conn.handleACK(b[1:])
	case b[0]&bitFlagNACK != 0:
		return conn.handleNACK(b[1:])
	case b[0]&bitFlagDatagram != 0:
		return conn.receiveDatagram(b[1:])
	}
	return nil
}

// receiveDatagram handles the receiving of a datagram found in buffer b. If
// successful, all packets inside the datagram are handled. if not, an error is
// returned.
func (conn *Conn) receiveDatagram(b []byte) error {
	if len(b) < 3 {
		return fmt.Errorf("read datagram: %w", io.ErrUnexpectedEOF)
	}
	seq := loadUint24(b)
	ok, skipped := conn.win.add(seq)
	if !ok {
		// A duplicate of a datagram still in the window. Not an error.
		return nil
	}
	conn.ackMu.Lock()
	// Add this sequence number to the received datagrams, so that it is
	// included in an ACK.
	if len(conn.ackSlice) == 0 {
		conn.oldestUnsentAck = time.Now()
		if conn.ackTimer == nil {
			conn.ackTimer = time.AfterFunc(ackDelay, conn.signalSend)
		} else {
			conn.ackTimer.Reset(ackDelay)
		}
	}
	conn.ackSlice = append(conn.ackSlice, seq)
	conn.ackMu.Unlock()
	conn.signalSend()

	if len(skipped) > 0 {
		// NACK the gap immediately, as the client does: a delayed report loses the
		// race against the sender's RTO, which cuts the window for a recoverable loss.
		if err := conn.sendNACK(skipped); err != nil {
			return fmt.Errorf("receive datagram: send NACK: %w", err)
		}
	}
	conn.win.shift()
	if conn.win.size() > maxWindowSize && conn.handler.limitsEnabled() {
		return fmt.Errorf("receive datagram: queue window size is too big (%v-%v)", conn.win.lowest, conn.win.highest)
	}
	return conn.handleDatagram(b[3:])
}

// handleDatagram handles the contents of a datagram encoded in a bytes.Buffer.
func (conn *Conn) handleDatagram(b []byte) error {
	for len(b) > 0 {
		n, err := conn.pk.read(b)
		if err != nil {
			return fmt.Errorf("handle datagram: read packet: %w", err)
		}
		b = b[n:]

		handle := conn.receivePacket
		if conn.pk.split {
			handle = conn.receiveSplitPacket
		}
		if err := handle(conn.pk); err != nil {
			return fmt.Errorf("handle datagram: receive packet: %w", err)
		}
	}
	return nil
}

// receivePacket handles the receiving of a packet. It puts the packet in the
// queue and takes out all packets that were obtainable after that, and handles
// them.
func (conn *Conn) receivePacket(packet *packet) error {
	if packet.reliability != reliabilityReliableOrdered {
		// If it isn't a reliable ordered packet, handle it immediately.
		return conn.handlePacket(packet.content)
	}
	if !conn.packetQueue.put(packet.orderIndex, packet.content) {
		// An ordered packet arrived twice.
		return nil
	}
	if conn.packetQueue.WindowSize() > maxWindowSize && conn.handler.limitsEnabled() {
		return fmt.Errorf("packet queue window size is too big (%v-%v)", conn.packetQueue.lowest, conn.packetQueue.highest)
	}
	for _, content := range conn.packetQueue.fetch() {
		if err := conn.handlePacket(content); err != nil {
			return err
		}
	}
	return nil
}

var errZeroPacket = errors.New("handle packet: zero packet length")

// handlePacket handles a packet serialised in byte slice b. If not successful,
// an error is returned. If the packet was not handled by RakNet, it is sent to
// the packet channel.
func (conn *Conn) handlePacket(b []byte) error {
	if len(b) == 0 {
		return errZeroPacket
	}
	if conn.closing.Load() != 0 {
		// Don't continue handling packets if the connection is being closed.
		return nil
	}
	handled, err := conn.handler.handle(conn, b)
	if err != nil {
		return fmt.Errorf("handle packet: %w", err)
	}
	if !handled {
		conn.packets.Send(b)
	}
	return nil
}

func resolve(addr net.Addr) netip.AddrPort {
	if udpAddr, ok := addr.(*net.UDPAddr); ok {
		uaddr := *udpAddr
		ip, _ := netip.AddrFromSlice(uaddr.IP)
		if ip.Is4In6() {
			ip = ip.Unmap()
		}
		return netip.AddrPortFrom(ip, uint16(uaddr.Port))
	}
	return netip.AddrPort{}
}

// receiveSplitPacket handles a passed split packet. If it is the last split
// packet of its sequence, it will continue handling the full packet as it
// otherwise would. An error is returned if the packet was not valid.
func (conn *Conn) receiveSplitPacket(p *packet) error {
	const maxSplitCount = 512
	const maxConcurrentSplits = 16

	if p.splitCount > maxSplitCount && conn.handler.limitsEnabled() {
		return fmt.Errorf("split packet: split count %v exceeds the maximum %v", p.splitCount, maxSplitCount)
	}
	if len(conn.splits) > maxConcurrentSplits && conn.handler.limitsEnabled() {
		return fmt.Errorf("split packet: maximum concurrent splits %v reached", maxConcurrentSplits)
	}
	m, ok := conn.splits[p.splitID]
	if !ok {
		m = make([][]byte, p.splitCount)
		conn.splits[p.splitID] = m
	}
	if p.splitIndex > uint32(len(m)-1) {
		// The split index was either negative or was bigger than the slice
		// size, meaning the packet is invalid.
		return fmt.Errorf("split packet: split index %v is out of range (0 - %v)", p.splitIndex, len(m)-1)
	}
	m[p.splitIndex] = p.content

	if slices.ContainsFunc(m, func(i []byte) bool { return len(i) == 0 }) {
		// We haven't yet received all split fragments, so we cannot add the
		// packets together yet.
		return nil
	}
	p.content = slices.Concat(m...)

	delete(conn.splits, p.splitID)
	return conn.receivePacket(p)
}

// sendACK sends an acknowledgement packet containing the packet sequence
// numbers passed. If not successful, an error is returned.
func (conn *Conn) sendACK(packets ...uint24) error {
	defer conn.ackBuf.Reset()
	return conn.sendAcknowledgement(packets, bitFlagACK, conn.ackBuf)
}

// sendNACK sends an acknowledgement packet containing the packet sequence
// numbers passed. If not successful, an error is returned.
func (conn *Conn) sendNACK(packets []uint24) error {
	defer conn.nackBuf.Reset()
	return conn.sendAcknowledgement(packets, bitFlagNACK, conn.nackBuf)
}

// sendAcknowledgement sends an acknowledgement packet with the packets passed,
// potentially sending multiple if too many packets are passed. The bitflag is
// added to the header byte.
func (conn *Conn) sendAcknowledgement(packets []uint24, bitflag byte, buf *bytes.Buffer) error {
	ack := &acknowledgement{packets: packets}

	for len(ack.packets) != 0 {
		buf.WriteByte(bitflag | bitFlagDatagram)
		n := ack.write(buf, conn.effectiveMTU())
		// We managed to write n packets in the ACK with this MTU size, write
		// the next of the packets in a new ACK.
		ack.packets = ack.packets[n:]
		if err := conn.writeTo(buf.Bytes(), conn.raddr); err != nil {
			return fmt.Errorf("send acknowlegement: %w", err)
		}
		buf.Reset()
	}
	return nil
}

// handleACK handles an acknowledgement packet from the other end of the
// connection. These mean that a datagram was successfully received by the
// other end.
func (conn *Conn) handleACK(b []byte) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	ack := &acknowledgement{}
	if err := ack.read(b); err != nil {
		return fmt.Errorf("read ACK: %w", err)
	}
	conn.ackedAny.Store(true)
	conn.congestion.continuous = conn.continuousSend
	for _, sequenceNumber := range ack.packets {
		if record, ok := conn.retransmission.acknowledge(sequenceNumber); ok {
			conn.congestion.acknowledged(record.inFlightBytes)
			conn.congestion.ack(sequenceNumber, conn.seq)
			record.pk.content = record.pk.content[:0]
			packetPool.Put(record.pk)
		}
	}
	// The window moved, so let the send loop recompute the budget and drain.
	conn.signalSend()
	return nil
}

// handleNACK handles a negative acknowledgment packet from the other end of
// the connection. These mean that a datagram was found missing.
func (conn *Conn) handleNACK(b []byte) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	nack := &acknowledgement{}
	if err := nack.read(b); err != nil {
		return fmt.Errorf("read NACK: %w", err)
	}
	conn.congestion.nak(conn.seq)
	// Bring the send timers forward so the next update retransmits them, which
	// is how the client turns a NAK into a resend.
	now := time.Now()
	for _, sequenceNumber := range nack.packets {
		record, ok := conn.retransmission.unacknowledged[sequenceNumber]
		if !ok {
			continue
		}
		record.nextSend = now
		conn.retransmission.unacknowledged[sequenceNumber] = record
		conn.retransmission.lowerDeadline(now)
	}
	conn.signalSend()
	return nil
}

// resend sends all datagrams currently in the recovery queue with the sequence
// numbers passed. A NAK reaches this through the same path as an expired timer,
// as it does on the client: the NAK already marked the congestion block as
// backed off, so congestionWindow.resend leaves the window alone for it.
func (conn *Conn) resend(sequenceNumbers []uint24) (err error) {
	// Retransmitted bytes already count as in flight. This budget caps one pass at
	// the outstanding total and protects against accounting drift.
	budget := conn.congestion.inFlight
	var sent uint32
	for _, sequenceNumber := range sequenceNumbers {
		pending, ok := conn.retransmission.unacknowledged[sequenceNumber]
		if !ok {
			continue
		}
		if pending.inFlightBytes > budget || sent > budget-pending.inFlightBytes {
			// Out of budget for this pass. Keep the remainder due so that the
			// next update retries them.
			conn.retransmission.lowerDeadline(pending.nextSend)
			break
		}
		record, ok := conn.retransmission.retransmit(sequenceNumber)
		if !ok {
			continue
		}
		conn.congestion.resend(conn.seq)
		if err = conn.sendDatagram(record.pk, record.inFlightBytes, true); err != nil {
			return err
		}
		sent += record.inFlightBytes
	}
	if sent >= conn.sendBudget {
		conn.sendBudget = 0
	}
	return nil
}

// sendable reports whether the next queued datagram fits in both the congestion
// window and the resend buffer.
func (conn *Conn) sendable(datagram queuedDatagram) bool {
	if conn.sendBudget == 0 {
		return false
	}
	if !datagram.pk.reliability.reliable() {
		return true
	}
	return len(conn.retransmission.unacknowledged) < resendBufferSize
}

// drainSendQueue sends what the window and resend buffer allow, control queue
// first. Only the send goroutine calls it.
func (conn *Conn) drainSendQueue() error {
	// Wake blocked writers once for the whole drain rather than per datagram:
	// they re-check the budget anyway, and each signal costs a channel.
	freed := false
	defer func() {
		if freed {
			conn.signalSendQueueFreed()
		}
	}()
	for len(conn.controlQueue) != 0 {
		if !conn.sendable(conn.controlQueue[0]) {
			return nil
		}
		datagram := conn.controlQueue[0]
		conn.controlQueue[0] = queuedDatagram{}
		conn.controlQueue = conn.controlQueue[1:]
		conn.sendQueueBytes -= datagram.wireSize
		freed = true
		conn.consumeSendBudget(datagram.inFlightBytes)
		if err := conn.sendDatagram(datagram.pk, datagram.inFlightBytes, false); err != nil {
			return err
		}
	}
	for len(conn.sendQueue) != 0 {
		if !conn.sendable(conn.sendQueue[0]) {
			return nil
		}
		datagram := conn.sendQueue[0]
		conn.sendQueue[0] = queuedDatagram{}
		conn.sendQueue = conn.sendQueue[1:]
		conn.sendQueueBytes -= datagram.wireSize
		freed = true
		conn.consumeSendBudget(datagram.inFlightBytes)
		if err := conn.sendDatagram(datagram.pk, datagram.inFlightBytes, false); err != nil {
			return err
		}
	}
	return nil
}

// consumeSendBudget subtracts sent bytes from this tick's budget, floored at zero.
func (conn *Conn) consumeSendBudget(size uint32) {
	if size >= conn.sendBudget {
		conn.sendBudget = 0
		return
	}
	conn.sendBudget -= size
}

// signalSendQueueFreed wakes every writer waiting for send queue space. It must
// be called with conn.mu held, which is what lets a writer capture the channel
// and release the lock without racing a drain.
func (conn *Conn) signalSendQueueFreed() {
	close(conn.sendQueueFreed)
	conn.sendQueueFreed = make(chan struct{})
}

// signalSend wakes the connection's send loop. Signals coalesce, so callers may
// signal freely.
func (conn *Conn) signalSend() {
	select {
	case conn.sendSignal <- struct{}{}:
	default:
	}
}

// sendDatagram numbers pk and writes it, tracking a reliable packet for resend
// and counting it in flight unless retransmit says it already was.
func (conn *Conn) sendDatagram(pk *packet, size uint32, retransmit bool) error {
	header := byte(bitFlagDatagram)
	if conn.congestion.slowStart() {
		// The client sets this from its own slow start state, asking the peer
		// to report bandwidth back while the window is still opening.
		header |= bitFlagNeedsBAndAS
	}
	if conn.wireContinuous {
		header |= bitFlagContinuousSend
	}
	conn.wireContinuous = true
	conn.buf.WriteByte(header)
	seq := conn.seq.Inc()
	writeUint24(conn.buf, seq)
	pk.write(conn.buf)
	defer conn.buf.Reset()

	if pk.reliability.reliable() {
		conn.retransmission.add(seq, pk, size)
		if !retransmit {
			conn.congestion.sent(size)
		}
	}

	if err := conn.writeTo(conn.buf.Bytes(), conn.raddr); err != nil {
		if pk.reliability.reliable() {
			delete(conn.retransmission.unacknowledged, seq)
			conn.congestion.acknowledged(size)
		}
		pk.content = pk.content[:0]
		packetPool.Put(pk)
		return fmt.Errorf("send datagram: %w", err)
	}
	if !pk.reliability.reliable() {
		pk.content = pk.content[:0]
		packetPool.Put(pk)
	}
	return nil
}

// writeTo calls WriteTo on the underlying UDP connection and returns an error
// only if the error returned is net.ErrClosed. In any other case, the error
// is logged but not returned. This is done because at this stage, packets
// being lost to an error can be recovered through resending.
func (conn *Conn) writeTo(p []byte, raddr net.Addr) error {
	if _, err := conn.conn.WriteTo(p, raddr); errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("write to: %w", err)
	} else if err != nil {
		conn.handler.log().Error("write to: "+err.Error(), "raddr", raddr.String())
	}
	return nil
}

// SystemUptime returns the uptime of the senders system or client.
func (conn *Conn) SystemUptime() time.Duration {
	return time.Since(conn.systemStart)
}

// startTime is the time the system or client was started.
var startTime = time.Now()

// timestamp returns a timestamp since startTime in milliseconds.
func timestamp() int64 {
	return time.Since(startTime).Milliseconds()
}
