package raknet

import (
	"bytes"
	"context"
	"fmt"
	"github.com/df-mc/atomic"
	"github.com/sandertv/go-raknet/internal/message"
	"net"
	"sync"
	"time"
)

const (
	// currentProtocol is the current RakNet protocol version. This is Minecraft specific.
	currentProtocol byte = 11

	maxMTUSize    = 1400
	maxWindowSize = 2048
)

// Conn represents a connection to a specific client. It is not a real connection, as UDP is connectionless,
// but rather a connection emulated using RakNet.
// Methods may be called on Conn from multiple goroutines simultaneously.
type Conn struct {
	// rtt is the last measured round-trip time between both ends of the connection. The rtt is measured in nanoseconds.
	rtt atomic.Int64

	closing atomic.Int64

	conn   net.PacketConn
	addr   net.Addr
	limits bool

	once              sync.Once
	closed, connected chan struct{}
	close             func()

	mu  sync.Mutex
	buf *bytes.Buffer

	ackBuf, nackBuf *bytes.Buffer

	pk *packet

	seq, orderIndex, messageIndex uint24
	splitID                       uint32

	// mtuSize is the MTU size of the connection. Packets longer than this size must be split into fragments
	// for them to arrive at the client without losing bytes.
	mtuSize uint16

	// splits is a map of slices indexed by split IDs. The length of each of the slices is equal to the split
	// count, and packets are positioned in that slice indexed by the split index.
	splits map[uint16][][]byte

	// win is an ordered queue used to track which datagrams were received and which datagrams
	// were missing, so that we can send NACKs to request missing datagrams.
	win *datagramWindow

	ackMu sync.Mutex
	// ackSlice is a slice containing sequence numbers of datagrams that were received over the last
	// second. When ticked, all of these packets are sent in an ACK and the slice is cleared.
	ackSlice []uint24

	// packetQueue is an ordered queue containing packets indexed by their order index.
	packetQueue *packetQueue
	// packets is a channel containing content of packets that were fully processed. Calling Conn.Read()
	// consumes a value from this channel.
	packets chan *bytes.Buffer

	// retransmission is a queue filled with packets that were sent with a given datagram sequence number.
	retransmission *resendMap

	// readDeadline is a channel that receives a time.Time after a specific time. It is used to listen for
	// timeouts in Read after calling SetReadDeadline.
	readDeadline <-chan time.Time

	lastActivity atomic.Value[time.Time]
}

// newConn constructs a new connection specifically dedicated to the address passed.
func newConn(conn net.PacketConn, addr net.Addr, mtuSize uint16) *Conn {
	return newConnWithLimits(conn, addr, mtuSize, true)
}

// newConnWithLimits returns a Conn for the net.Addr passed with a specific mtu size. The limits bool passed specifies
// if the connection should limit the bounds of things such as the size of packets. This is generally recommended for
// connections coming from a client.
func newConnWithLimits(conn net.PacketConn, addr net.Addr, mtuSize uint16, limits bool) *Conn {
	if mtuSize < 500 || mtuSize > 1500 {
		mtuSize = maxMTUSize
	}
	c := &Conn{
		addr:           addr,
		conn:           conn,
		limits:         limits,
		mtuSize:        mtuSize,
		pk:             new(packet),
		closed:         make(chan struct{}),
		connected:      make(chan struct{}),
		packets:        make(chan *bytes.Buffer, 512),
		splits:         make(map[uint16][][]byte),
		win:            newDatagramWindow(),
		packetQueue:    newPacketQueue(),
		retransmission: newRecoveryQueue(),
		buf:            bytes.NewBuffer(make([]byte, 0, mtuSize)),
		ackBuf:         bytes.NewBuffer(make([]byte, 0, 256)),
		nackBuf:        bytes.NewBuffer(make([]byte, 0, 256)),
		lastActivity:   *atomic.NewValue(time.Now()),
	}
	go c.startTicking()
	return c
}

// startTicking makes the connection start ticking, sending ACKs and pings to the other end where necessary
// and checking if the connection should be timed out.
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
		case t := <-ticker.C:
			i++
			conn.flushACKs()
			if i%2 == 0 {
				// We send a connected ping to calculate the rtt and let the other side know we haven't
				// timed out.
				conn.sendPing()
			}
			if i%3 == 0 {
				conn.checkResend(t)
			}
			if i%5 == 0 {
				conn.mu.Lock()
				if t.Sub(conn.lastActivity.Load()) > time.Second*5+conn.retransmission.rtt()*2 {
					// No activity for too long: Start timeout.
					_ = conn.Close()
				}
				conn.mu.Unlock()
			}
			if unix := conn.closing.Load(); unix != 0 {
				before := acksLeft
				conn.mu.Lock()
				acksLeft = len(conn.retransmission.unacknowledged)
				conn.mu.Unlock()

				if before != 0 && acksLeft == 0 {
					_ = conn.Close()
				}

				since := time.Since(time.Unix(unix, 0))
				if (acksLeft == 0 && since > time.Second) || since > time.Second*8 {
					conn.closeImmediately()
				}
			}
		case <-conn.closed:
			return
		}
	}
}

// flushACKs flushes all pending datagram acknowledgements.
func (conn *Conn) flushACKs() {
	conn.ackMu.Lock()
	defer conn.ackMu.Unlock()

	if len(conn.ackSlice) > 0 {
		// Write an ACK packet to the connection containing all datagram sequence numbers that we
		// received since the last tick.
		if err := conn.sendACK(conn.ackSlice...); err != nil {
			return
		}
		conn.ackSlice = conn.ackSlice[:0]
	}
}

// checkResend checks if the connection needs to resend any packets. It sends an ACK for packets it has
// received and sends any packets that have been pending for too long.
func (conn *Conn) checkResend(now time.Time) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	var (
		resend []uint24
		rtt    = conn.retransmission.rtt()
		delay  = rtt + rtt/2
	)
	conn.rtt.Store(int64(rtt))

	for seq, t := range conn.retransmission.unacknowledged {
		// These packets have not been acknowledged for too long: We resend them by ourselves, even though no
		// NACK has been issued yet.
		if now.Sub(t.timestamp) > delay {
			resend = append(resend, seq)
		}
	}
	_ = conn.resend(resend)
}

// Write writes a buffer b over the RakNet connection. The amount of bytes written n is always equal to the
// length of the bytes written if the write was successful. If not, an error is returned and n is 0.
// Write may be called simultaneously from multiple goroutines, but will write one by one.
func (conn *Conn) Write(b []byte) (n int, err error) {
	select {
	case <-conn.closed:
		return 0, conn.wrap(net.ErrClosed, "write")
	default:
		conn.mu.Lock()
		defer conn.mu.Unlock()
		n, err := conn.write(b)
		return n, conn.wrap(err, "write")
	}
}

// write writes a buffer b over the RakNet connection. The amount of bytes written n is always equal to the
// length of the bytes written if the write was successful. If not, an error is returned and n is 0.
// Write may be called simultaneously from multiple goroutines, but will write one by one.
// Unlike Write, write will not lock.
func (conn *Conn) write(b []byte) (n int, err error) {
	fragments := conn.split(b)
	orderIndex := conn.orderIndex
	conn.orderIndex++

	splitID := uint16(conn.splitID)
	split := len(fragments) > 1
	if split {
		conn.splitID++
	}
	for splitIndex, content := range fragments {
		sequenceNumber := conn.seq
		conn.seq++
		messageIndex := conn.messageIndex
		conn.messageIndex++

		conn.buf.WriteByte(bitFlagDatagram | bitFlagNeedsBAndAS)
		writeUint24(conn.buf, sequenceNumber)
		pk := packetPool.Get().(*packet)
		if cap(pk.content) < len(content) {
			pk.content = make([]byte, len(content))
		}
		// We set the actual slice size to the same size as the content. It might be bigger than the previous
		// size, in which case it will grow, which is fine as the underlying array will always be big enough.
		pk.content = pk.content[:len(content)]
		copy(pk.content, content)

		pk.orderIndex = orderIndex
		pk.messageIndex = messageIndex

		pk.split = split
		if split {
			// If there were more than one fragment, the pk was split, so we need to make sure we set the
			// appropriate fields.
			pk.splitCount = uint32(len(fragments))
			pk.splitIndex = uint32(splitIndex)
			pk.splitID = splitID
		}
		pk.write(conn.buf)
		// We then send the pk to the connection.
		if _, err := conn.conn.WriteTo(conn.buf.Bytes(), conn.addr); err != nil {
			return 0, net.ErrClosed
		}

		// We reset the buffer so that we can re-use it for each fragment created when splitting the pk.
		conn.buf.Reset()

		// Finally we add the pk to the recovery queue.
		conn.retransmission.add(sequenceNumber, pk)
		n += len(content)
	}
	return
}

// Read reads from the connection into the byte slice passed. If successful, the amount of bytes read n is
// returned, and the error returned will be nil.
// Read blocks until a packet is received over the connection, or until the session is closed or the read
// times out, in which case an error is returned.
func (conn *Conn) Read(b []byte) (n int, err error) {
	select {
	case pk := <-conn.packets:
		if len(b) < pk.Len() {
			err = conn.wrap(errBufferTooSmall, "read")
		}
		return copy(b, pk.Bytes()), err
	case <-conn.closed:
		return 0, conn.wrap(net.ErrClosed, "read")
	case <-conn.readDeadline:
		return 0, conn.wrap(context.DeadlineExceeded, "read")
	}
}

// ReadPacket attempts to read the next packet as a byte slice.
// ReadPacket blocks until a packet is received over the connection, or until the session is closed or the
// read times out, in which case an error is returned.
func (conn *Conn) ReadPacket() (b []byte, err error) {
	select {
	case packet := <-conn.packets:
		return packet.Bytes(), err
	case <-conn.closed:
		return nil, conn.wrap(net.ErrClosed, "read")
	case <-conn.readDeadline:
		return nil, conn.wrap(context.DeadlineExceeded, "read")
	}
}

// Close closes the connection. All blocking Read or Write actions are cancelled and will return an error, as
// soon as the closing of the connection is acknowledged by the client.
func (conn *Conn) Close() error {
	conn.closing.CAS(0, time.Now().Unix())
	return nil
}

// closeImmediately sends a Disconnect notification to the other end of the connection and
// closes the underlying UDP connection immediately.
func (conn *Conn) closeImmediately() {
	conn.once.Do(func() {
		_, _ = conn.Write([]byte{message.IDDisconnectNotification})
		close(conn.closed)
		if conn.close != nil {
			conn.close()
			conn.close = nil
		}
	})
}

// RemoteAddr returns the remote address of the connection, meaning the address this connection leads to.
func (conn *Conn) RemoteAddr() net.Addr {
	return conn.addr
}

// LocalAddr returns the local address of the connection, which is always the same as the listener's.
func (conn *Conn) LocalAddr() net.Addr {
	return conn.conn.LocalAddr()
}

// SetReadDeadline sets the read deadline of the connection. An error is returned only if the time passed is
// before time.Now().
// Calling SetReadDeadline means the next Read call that exceeds the deadline will fail and return an error.
// Setting the read deadline to the default value of time.Time removes the deadline.
func (conn *Conn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		conn.readDeadline = make(chan time.Time)
		return nil
	}
	if t.Before(time.Now()) {
		panic(fmt.Errorf("read deadline cannot be before now"))
	}
	conn.readDeadline = time.After(time.Until(t))
	return nil
}

// SetWriteDeadline has no behaviour. It is merely there to satisfy the net.Conn interface.
func (conn *Conn) SetWriteDeadline(time.Time) error {
	return nil
}

// SetDeadline sets the deadline of the connection for both Read and Write. SetDeadline is equivalent to
// calling both SetReadDeadline and SetWriteDeadline.
func (conn *Conn) SetDeadline(t time.Time) error {
	return conn.SetReadDeadline(t)
}

// Latency returns a rolling average of rtt between the sending and the receiving end of the connection.
// The rtt returned is updated continuously and is half the average round trip time (RTT).
func (conn *Conn) Latency() time.Duration {
	return time.Duration(conn.rtt.Load() / 2)
}

// sendPing pings the connection, updating the rtt of the Conn if successful.
func (conn *Conn) sendPing() {
	b := bytes.NewBuffer(nil)
	(&message.ConnectedPing{ClientTimestamp: timestamp()}).Write(b)
	_, _ = conn.Write(b.Bytes())
}

// packetPool is a sync.Pool used to pool packets that encapsulate their content.
var packetPool = sync.Pool{
	New: func() interface{} {
		return &packet{reliability: reliabilityReliableOrdered}
	},
}

const (
	// Datagram header +
	// Datagram sequence number +
	// Packet header +
	// Packet content length +
	// Packet message index +
	// Packet order index +
	// Packet order channel
	packetAdditionalSize = 1 + 3 + 1 + 2 + 3 + 3 + 1
	// Packet split count +
	// Packet split ID +
	// Packet split index
	splitAdditionalSize = 4 + 2 + 4
)

// split splits a content buffer in smaller buffers so that they do not exceed the MTU size that the
// connection holds.
func (conn *Conn) split(b []byte) [][]byte {
	maxSize := int(conn.mtuSize-packetAdditionalSize) - 28
	contentLength := len(b)
	if contentLength > maxSize {
		// If the content size is bigger than the maximum size here, it means the packet will get split. This
		// means that the packet will get even bigger because a split packet uses 4 + 2 + 4 more bytes.
		maxSize -= splitAdditionalSize
	}
	fragmentCount := contentLength / maxSize
	if contentLength%maxSize != 0 {
		// If the content length can't be divided by maxSize perfectly, we need to reserve another fragment
		// for the last bit of the packet.
		fragmentCount++
	}
	fragments := make([][]byte, fragmentCount)

	buf := bytes.NewBuffer(b)
	for i := 0; i < fragmentCount; i++ {
		// Take a piece out of the content with the size of maxSize.
		fragments[i] = buf.Next(maxSize)
	}
	return fragments
}

// receive receives a packet from the connection, handling it as appropriate. If not successful, an error is
// returned.
func (conn *Conn) receive(b *bytes.Buffer) error {
	headerFlags, err := b.ReadByte()
	if err != nil {
		return fmt.Errorf("error reading datagram header flags: %v", err)
	}
	if headerFlags&bitFlagDatagram == 0 {
		// Ignore packets that do not have the datagram bitflag.
		return nil
	}
	conn.lastActivity.Store(time.Now())
	switch {
	case headerFlags&bitFlagACK != 0:
		return conn.handleACK(b)
	case headerFlags&bitFlagNACK != 0:
		return conn.handleNACK(b)
	default:
		return conn.receiveDatagram(b)
	}
}

// receiveDatagram handles the receiving of a datagram found in buffer b. If successful, all packets inside
// the datagram are handled. if not, an error is returned.
func (conn *Conn) receiveDatagram(b *bytes.Buffer) error {
	seq, err := readUint24(b)
	if err != nil {
		return fmt.Errorf("error reading datagram sequence number: %v", err)
	}
	conn.ackMu.Lock()
	// Add this sequence number to the received datagrams, so that it is included in an ACK.
	conn.ackSlice = append(conn.ackSlice, seq)
	conn.ackMu.Unlock()

	if !conn.win.new(seq) {
		// Datagram was already received, this might happen if a packet took a long time to arrive, and we already sent
		// a NACK for it. This is expected to happen sometimes under normal circumstances, so no reason to return an
		// error.
		return nil
	}
	conn.win.add(seq)
	if conn.win.shift() == 0 {
		// Datagram window couldn't be shifted up, so we're still missing packets.
		rtt := time.Duration(conn.rtt.Load())
		if missing := conn.win.missing(rtt + rtt/2); len(missing) > 0 {
			if err = conn.sendNACK(missing); err != nil {
				return fmt.Errorf("error sending NACK to request datagrams: %v", err)
			}
		}
	}
	if conn.win.size() > maxWindowSize && conn.limits {
		return fmt.Errorf("datagram receive queue window size is too big (%v-%v)", conn.win.lowest, conn.win.highest)
	}
	return conn.handleDatagram(b)
}

// handleDatagram handles the contents of a datagram encoded in a bytes.Buffer.
func (conn *Conn) handleDatagram(b *bytes.Buffer) error {
	for b.Len() > 0 {
		if err := conn.pk.read(b); err != nil {
			return fmt.Errorf("error decoding datagram packet: %v", err)
		}
		handle := conn.receivePacket
		if conn.pk.split {
			handle = conn.receiveSplitPacket
		}
		if err := handle(conn.pk); err != nil {
			return fmt.Errorf("error handling packet in datagram: %v", err)
		}
	}
	return nil
}

// receivePacket handles the receiving of a packet. It puts the packet in the queue and takes out all packets
// that were obtainable after that, and handles them.
func (conn *Conn) receivePacket(packet *packet) error {
	if packet.reliability != reliabilityReliableOrdered {
		// If it isn't a reliable ordered packet, handle it immediately.
		return conn.handlePacket(packet.content)
	}
	if !conn.packetQueue.put(packet.orderIndex, packet.content) {
		// An ordered packet arrived twice.
		return nil
	}
	if conn.packetQueue.WindowSize() > maxWindowSize && conn.limits {
		return fmt.Errorf("packet queue window size is too big (%v-%v)", conn.packetQueue.lowest, conn.packetQueue.highest)
	}
	for _, content := range conn.packetQueue.fetch() {
		if err := conn.handlePacket(content); err != nil {
			return fmt.Errorf("error handling packet: %v", err)
		}
	}
	return nil
}

// handlePacket handles a packet serialised in byte slice b. If not successful, an error is returned. If the
// packet was not handled by RakNet, it is sent to the packet channel.
func (conn *Conn) handlePacket(b []byte) error {
	buffer := bytes.NewBuffer(b)
	id, err := buffer.ReadByte()
	if err != nil {
		return fmt.Errorf("error reading packet ID: %v", err)
	}

	switch id {
	case message.IDConnectionRequest:
		return conn.handleConnectionRequest(buffer)
	case message.IDConnectionRequestAccepted:
		return conn.handleConnectionRequestAccepted(buffer)
	case message.IDNewIncomingConnection:
		select {
		case <-conn.connected:
		default:
			close(conn.connected)
		}
	case message.IDConnectedPing:
		return conn.handleConnectedPing(buffer)
	case message.IDConnectedPong:
		return conn.handleConnectedPong(buffer)
	case message.IDDisconnectNotification:
		conn.closeImmediately()
	case message.IDDetectLostConnections:
		// Let the other end know the connection is still alive.
		conn.sendPing()
	default:
		_ = buffer.UnreadByte()
		// Insert the packet contents the packet queue could release in the channel so that Conn.Read() can
		// get a hold of them, but always first try to escape if the connection was closed.
		select {
		case <-conn.closed:
		case conn.packets <- buffer:
		}
	}
	return nil
}

// handleConnectedPing handles a connected ping packet inside of buffer b. An error is returned if the packet
// was invalid.
func (conn *Conn) handleConnectedPing(b *bytes.Buffer) error {
	packet := &message.ConnectedPing{}
	if err := packet.Read(b); err != nil {
		return fmt.Errorf("error reading connected ping: %v", err)
	}
	b.Reset()

	// Respond with a connected pong that has the ping timestamp found in the connected ping, and our own
	// timestamp for the pong timestamp.
	(&message.ConnectedPong{ClientTimestamp: packet.ClientTimestamp, ServerTimestamp: timestamp()}).Write(b)
	_, err := conn.Write(b.Bytes())
	return err
}

// handleConnectedPong handles a connected pong packet inside of buffer b. An error is returned if the packet
// was invalid.
func (conn *Conn) handleConnectedPong(b *bytes.Buffer) error {
	packet := &message.ConnectedPong{}
	if err := packet.Read(b); err != nil {
		return fmt.Errorf("error reading connected pong: %v", err)
	}
	if packet.ClientTimestamp > timestamp() {
		return fmt.Errorf("error measuring rtt: ping timestamp is in the future")
	}
	// We don't actually use the ConnectedPong to measure rtt. It is too unreliable and doesn't give a
	// good idea of the connection quality.
	return nil
}

// handleConnectionRequest handles a connection request packet inside of buffer b. An error is returned if the
// packet was invalid.
func (conn *Conn) handleConnectionRequest(b *bytes.Buffer) error {
	packet := &message.ConnectionRequest{}
	if err := packet.Read(b); err != nil {
		return fmt.Errorf("error reading connection request: %v", err)
	}
	b.Reset()
	(&message.ConnectionRequestAccepted{ClientAddress: *conn.addr.(*net.UDPAddr), RequestTimestamp: packet.RequestTimestamp, AcceptedTimestamp: timestamp()}).Write(b)
	_, err := conn.Write(b.Bytes())
	return err
}

// handleConnectionRequestAccepted handles a serialised connection request accepted packet in b, and returns
// an error if not successful.
func (conn *Conn) handleConnectionRequestAccepted(b *bytes.Buffer) error {
	packet := &message.ConnectionRequestAccepted{}
	_ = packet.Read(b)
	b.Reset()

	(&message.NewIncomingConnection{ServerAddress: *conn.addr.(*net.UDPAddr), RequestTimestamp: packet.RequestTimestamp, AcceptedTimestamp: packet.AcceptedTimestamp, SystemAddresses: packet.SystemAddresses}).Write(b)
	_, err := conn.Write(b.Bytes())

	select {
	case <-conn.connected:
	default:
		close(conn.connected)
	}
	return err
}

// receiveSplitPacket handles a passed split packet. If it is the last split packet of its sequence, it will
// continue handling the full packet as it otherwise would.
// An error is returned if the packet was not valid.
func (conn *Conn) receiveSplitPacket(p *packet) error {
	const maxSplitCount = 256
	if (p.splitCount > maxSplitCount || len(conn.splits) > maxSplitCount) && conn.limits {
		return fmt.Errorf("split count %v (%v active) exceeds the maximum %v", p.splitCount, len(conn.splits), maxSplitCount)
	}
	m, ok := conn.splits[p.splitID]
	if !ok {
		m = make([][]byte, p.splitCount)
		conn.splits[p.splitID] = m
	}
	if p.splitIndex > uint32(len(m)-1) {
		// The split index was either negative or was bigger than the slice size, meaning the packet is
		// invalid.
		return fmt.Errorf("error handing split packet: split index %v is out of range (0 - %v)", p.splitIndex, len(m)-1)
	}
	m[p.splitIndex] = p.content

	size := 0
	for _, fragment := range m {
		if len(fragment) == 0 {
			// We haven't yet received all split fragments, so we cannot add the packets together yet.
			return nil
		}
		// First we calculate the total size required to hold the content of the combined content.
		size += len(fragment)
	}

	content := make([]byte, 0, size)
	for _, fragment := range m {
		content = append(content, fragment...)
	}

	delete(conn.splits, p.splitID)

	p.content = content
	return conn.receivePacket(p)
}

// sendACK sends an acknowledgement packet containing the packet sequence numbers passed. If not successful,
// an error is returned.
func (conn *Conn) sendACK(packets ...uint24) error {
	defer conn.ackBuf.Reset()
	return conn.sendAcknowledgement(packets, bitFlagACK, conn.ackBuf)
}

// sendNACK sends an acknowledgement packet containing the packet sequence numbers passed. If not successful,
// an error is returned.
func (conn *Conn) sendNACK(packets []uint24) error {
	defer conn.nackBuf.Reset()
	return conn.sendAcknowledgement(packets, bitFlagNACK, conn.nackBuf)
}

// sendAcknowledgement sends an acknowledgement packet with the packets passed, potentially sending multiple
// if too many packets are passed. The bitflag is added to the header byte.
func (conn *Conn) sendAcknowledgement(packets []uint24, bitflag byte, buf *bytes.Buffer) error {
	ack := &acknowledgement{packets: packets}

	for len(ack.packets) != 0 {
		buf.WriteByte(bitflag | bitFlagDatagram)
		n, err := ack.write(buf, conn.mtuSize)
		if err != nil {
			panic(fmt.Sprintf("error encoding ACK packet: %v", err))
		}
		// We managed to write n packets in the ACK with this MTU size, write the next of the packets in a new ACK.
		ack.packets = ack.packets[n:]
		if _, err := conn.conn.WriteTo(buf.Bytes(), conn.addr); err != nil {
			return fmt.Errorf("error sending ACK packet: %v", err)
		}
		buf.Reset()
	}
	return nil
}

// handleACK handles an acknowledgement packet from the other end of the connection. These mean that a
// datagram was successfully received by the other end.
func (conn *Conn) handleACK(b *bytes.Buffer) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	ack := &acknowledgement{}
	if err := ack.read(b); err != nil {
		return fmt.Errorf("error reading ACK: %v", err)
	}
	for _, sequenceNumber := range ack.packets {
		// Take out all stored packets from the recovery queue.
		p, ok := conn.retransmission.acknowledge(sequenceNumber)
		if ok {
			// Clear the packet and return it to the pool so that it may be re-used.
			p.content = nil
			packetPool.Put(p)
		}
	}
	return nil
}

// handleNACK handles a negative acknowledgment packet from the other end of the connection. These mean that a
// datagram was found missing.
func (conn *Conn) handleNACK(b *bytes.Buffer) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	nack := &acknowledgement{}
	if err := nack.read(b); err != nil {
		return fmt.Errorf("error reading NACK: %v", err)
	}
	return conn.resend(nack.packets)
}

// resend sends all datagrams currently in the recovery queue with the sequence numbers passed.
func (conn *Conn) resend(sequenceNumbers []uint24) (err error) {
	for _, sequenceNumber := range sequenceNumbers {
		pk, ok := conn.retransmission.retransmit(sequenceNumber)
		if !ok {
			// We could not resend this datagram. Maybe it was already resent before at the request of the
			// client. This is generally expected so we just continue.
			continue
		}

		// We first write a new datagram header using a new send sequence number that we find.
		if err := conn.buf.WriteByte(bitFlagDatagram | bitFlagNeedsBAndAS); err != nil {
			return fmt.Errorf("error writing recovered datagram header: %v", err)
		}
		newSeqNum := conn.seq
		conn.seq++
		writeUint24(conn.buf, newSeqNum)
		pk.write(conn.buf)

		// We then send the pk to the connection.
		if _, err := conn.conn.WriteTo(conn.buf.Bytes(), conn.addr); err != nil {
			return fmt.Errorf("error sending pk to addr %v: %v", conn.addr, err)
		}
		// We then re-add the pk to the recovery queue in case the new one gets lost too, in which case
		// we need to resend it again.
		conn.retransmission.add(newSeqNum, pk)
		conn.buf.Reset()
	}
	return nil
}

// requestConnection requests the connection from the server, provided this connection operates as a client.
// An error occurs if the request was not successful.
func (conn *Conn) requestConnection(id int64) error {
	b := bytes.NewBuffer(nil)
	(&message.ConnectionRequest{ClientGUID: id, RequestTimestamp: timestamp()}).Write(b)
	_, err := conn.Write(b.Bytes())
	return err
}
