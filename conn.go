package raknet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/sandertv/go-raknet/internal/message"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// currentProtocol is the current RakNet protocol version. This is Minecraft specific.
	currentProtocol byte = 10

	// timeout is the timeout after which a conn times out, if it hasn't received a packet for that duration.
	timeout = time.Second * 10
	// resendRequestThreshold is the amount of datagrams that must be received before datagrams that were
	// missing earlier will be requested to be resent.
	resendRequestThreshold = 10
	// tickInterval is the interval at which the connection sends an ACK containing the packets which were
	// received or a NACK for missing packets.
	tickInterval = time.Second / 20
	// pingInterval is the interval in seconds at which a ping is sent to the other end of the connection.
	pingInterval = time.Second * 4

	maxMTUSize    = 1400
	maxWindowSize = 1024
)

var (
	errConnectionClosed = "error reading from conn: connection closed"
	errUseOfClosed      = "use of closed network connection"
	errReadTimeout      = "error reading from conn: read timeout"
)

// ErrConnectionClosed checks if the error passed was an error caused by reading from a Conn of which the
// connection was closed.
func ErrConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), errConnectionClosed) || strings.Contains(err.Error(), errUseOfClosed)
}

// ErrReadTimeout checks if the error passed was an error caused by a timeout set when reading from the Conn.
//noinspection GoUnusedExportedFunction
func ErrReadTimeout(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), errReadTimeout)
}

// Conn represents a connection to a specific client. It is not a real connection, as UDP is connectionless,
// but rather a connection emulated using RakNet.
// Methods may be called on Conn from multiple goroutines simultaneously.
type Conn struct {
	conn   net.PacketConn
	client bool
	addr   net.Addr

	writeLock sync.Mutex
	writeBuf  *bytes.Buffer

	ackBuf, nackBuf *bytes.Buffer

	readPacket *packet

	sendSequenceNumber uint24
	sendOrderIndex     uint24
	sendMessageIndex   uint24
	sendSplitID        uint32

	// completingSequence is a Context which is completed once the RakNet connection sequence is completed.
	completingSequence context.Context
	finishSequence     context.CancelFunc
	connected          bool

	// id is the random client GUID of the client. It is different each time a client connects to to a server.
	id int64
	// mtuSize is the MTU size of the connection. Packets longer than this size must be split into fragments
	// for them to arrive at the client without losing bytes.
	mtuSize int16

	// latency is the last measured latency between both ends of the connection. Note that this latency is
	// not the round-trip time, but half of that. The latency is measured in nanoseconds.
	latency uint32

	// splits is a map of slices indexed by split IDs. The length of each of the slices is equal to the split
	// count, and packets are positioned in that slice indexed by the split index.
	splits map[uint16][][]byte

	// datagramQueue is an ordered queue used to track which datagrams were received and which datagrams
	// were missing, so that we can send NACKs to request missing datagrams.
	datagramQueue *datagramQueue
	// datagramsReceived is a slice containing sequence numbers of datagrams that were received over the last
	// 3 seconds. When ticked, all of these packets are sent in an ACK and the slice is cleared.
	datagramsReceived atomic.Value
	// missingDatagramTimes is the times that a datagram was received, but a previous datagram was not.
	missingDatagramTimes int

	// packetQueue is an ordered queue containing packets indexed by their order index.
	packetQueue *packetQueue
	// packetChan is a channel containing content of packets that were fully processed. Calling Conn.Read()
	// consumes a value from this channel.
	packetChan chan *bytes.Buffer
	// lastPacketTime is the last time a packet was received. It is used to measure the time until the
	// connection times out.
	lastPacketTime atomic.Value

	// recoveryQueue is a queue filled with packets that were sent with a given datagram sequence number.
	recoveryQueue *recoveryQueue

	closeCtx context.Context
	once     sync.Once
	close    context.CancelFunc

	// readDeadline is a channel that receives a time.Time after a specific time. It is used to listen for
	// timeouts in Read after calling SetReadDeadline.
	readDeadline <-chan time.Time

	resends int
}

// newConn constructs a new connection specifically dedicated to the address passed.
func newConn(conn net.PacketConn, addr net.Addr, mtuSize int16, id int64, client bool) *Conn {
	if mtuSize < 500 || mtuSize > 1500 {
		mtuSize = maxMTUSize
	}
	ctx, cancel := context.WithCancel(context.Background())
	sequenceCtx, sequenceComplete := context.WithCancel(context.Background())
	c := &Conn{
		client:             client,
		addr:               addr,
		conn:               conn,
		mtuSize:            mtuSize,
		id:                 id,
		completingSequence: sequenceCtx,
		finishSequence:     sequenceComplete,
		splits:             make(map[uint16][][]byte),
		datagramQueue:      newDatagramQueue(),
		packetQueue:        newPacketQueue(),
		recoveryQueue:      newRecoveryQueue(),
		close:              cancel,
		closeCtx:           ctx,
		packetChan:         make(chan *bytes.Buffer, 512),
		writeBuf:           bytes.NewBuffer(nil),
		ackBuf:             bytes.NewBuffer(make([]byte, 0, 256)),
		nackBuf:            bytes.NewBuffer(make([]byte, 0, 256)),
		readPacket:         &packet{},
	}
	c.lastPacketTime.Store(time.Now())
	c.datagramsReceived.Store([]uint24{})
	go c.startTicking()

	return c
}

// startTicking makes the connection start ticking, sending ACKs and pings to the other end where necessary
// and checking if the connection should be timed out.
func (conn *Conn) startTicking() {
	ticker, pingTicker := time.NewTicker(tickInterval), time.NewTicker(pingInterval)
	defer ticker.Stop()
	defer pingTicker.Stop()
	for {
		select {
		case <-pingTicker.C:
			// We send a connected ping to calculate the latency and let the other side know we haven't
			// timed out.
			conn.sendPing()
		case t := <-ticker.C:
			// We first check if the other end has actually timed out. If so, we close the conn, as it is
			// likely the client was disconnected.
			if t.Sub(conn.lastPacketTime.Load().(time.Time)) > timeout {
				// If the timeout was long enough, we close the connection.
				_ = conn.Close()
				return
			}
			conn.checkResend()
		case <-conn.closeCtx.Done():
			return
		}
	}
}

// checkResend checks if the connection needs to resend any packets. It sends an ACK for packets it has
// received and sends any packets that have been pending for too long.
func (conn *Conn) checkResend() {
	received := conn.datagramsReceived.Load().([]uint24)
	if len(received) > 0 {
		// Write an ACK packet to the connection containing all datagram sequence numbers that we
		// received since the last tick.
		if err := conn.sendACK(received...); err != nil {
			return
		}
		conn.datagramsReceived.Store(received[:0])
	}
	conn.writeLock.Lock()
	var resendSeqNums []uint24

	latency := conn.recoveryQueue.AvgDelay()
	atomic.StoreUint32(&conn.latency, uint32(latency/2))

	// Allow the average delay with a deviation of 200%.
	delay := latency*3 + ((latency / 4) * time.Duration(conn.resends))
	for seqNum := range conn.recoveryQueue.queue {
		// These packets have not been acknowledged for too long: We resend them by ourselves, even though no
		// NACK has been issued yet.
		if time.Since(conn.recoveryQueue.Timestamp(seqNum)) > delay {
			resendSeqNums = append(resendSeqNums, seqNum)
		}
	}
	l := len(resendSeqNums)
	_ = conn.resend(resendSeqNums) // NOP if len(resendSeqNums) == 0.
	conn.writeLock.Unlock()

	if l != 0 {
		conn.resends++

		if conn.resends > 30 {
			_ = conn.Close()
		}
	} else {
		conn.resends--
	}
}

// Write writes a buffer b over the RakNet connection. The amount of bytes written n is always equal to the
// length of the bytes written if the write was successful. If not, an error is returned and n is 0.
// Write may be called simultaneously from multiple goroutines, but will write one by one.
func (conn *Conn) Write(b []byte) (n int, err error) {
	select {
	case <-conn.closeCtx.Done():
		return 0, errors.New(errConnectionClosed)
	default:
		conn.writeLock.Lock()
		defer conn.writeLock.Unlock()
		return conn.write(b)
	}
}

// write writes a buffer b over the RakNet connection. The amount of bytes written n is always equal to the
// length of the bytes written if the write was successful. If not, an error is returned and n is 0.
// Write may be called simultaneously from multiple goroutines, but will write one by one.
// Unlike Write, write will not lock.
func (conn *Conn) write(b []byte) (n int, err error) {
	fragments := conn.split(b)
	orderIndex := conn.sendOrderIndex
	conn.sendOrderIndex++

	splitID := uint16(conn.sendSplitID)
	if len(fragments) > 1 {
		conn.sendSplitID++
	}
	for splitIndex, content := range fragments {
		sequenceNumber := conn.sendSequenceNumber
		conn.sendSequenceNumber++
		messageIndex := conn.sendMessageIndex
		conn.sendMessageIndex++

		if err := conn.writeBuf.WriteByte(bitFlagDatagram); err != nil {
			return 0, fmt.Errorf("error writing datagram header: %v", err)
		}
		writeUint24(conn.writeBuf, sequenceNumber)
		packet := packetPool.Get().(*packet)
		if cap(packet.content) < len(content) {
			packet.content = make([]byte, len(content))
		}
		// We set the actual slice size to the same size as the content. It might be bigger than the previous
		// size, in which case it will grow, which is fine as the underlying array will always be big enough.
		packet.content = packet.content[:len(content)]
		copy(packet.content, content)

		packet.orderIndex = orderIndex
		packet.messageIndex = messageIndex

		if len(fragments) > 1 {
			// If there were more than one fragment, the packet was split, so we need to make sure we set the
			// appropriate fields.
			packet.split = true
			packet.splitCount = uint32(len(fragments))
			packet.splitIndex = uint32(splitIndex)
			packet.splitID = splitID
		} else {
			packet.split = false
		}
		if err := packet.write(conn.writeBuf); err != nil {
			return 0, fmt.Errorf("error writing packet to buffer: %v", err)
		}
		// We then send the packet to the connection.
		if _, err := conn.conn.WriteTo(conn.writeBuf.Bytes(), conn.addr); err != nil {
			return 0, fmt.Errorf("error sending packet to addr %v: %v", conn.addr, err)
		}
		// We reset the buffer so that we can re-use it for each fragment created when splitting the packet.
		conn.writeBuf.Reset()

		// Finally we add the packet to the recovery queue.
		conn.recoveryQueue.put(sequenceNumber, packet)
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
	case packet := <-conn.packetChan:
		if len(b) < packet.Len() {
			err = fmt.Errorf("raknet.Conn read: read raknet: A message sent on a RakNet socket was larger than the buffer used to receive the message into")
		}
		return copy(b, packet.Bytes()), err
	case <-conn.closeCtx.Done():
		return 0, errors.New(errConnectionClosed)
	case <-conn.readDeadline:
		return 0, errors.New(errReadTimeout)
	}
}

// Close closes the connection. All blocking Read or Write actions are cancelled and will return an error.
func (conn *Conn) Close() error {
	conn.once.Do(func() {
		_, _ = conn.Write([]byte{message.IDDisconnectNotification})
		conn.close()
	})
	return nil
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
		return fmt.Errorf("read deadline cannot be before now")
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

// Latency returns a rolling average of latency between the sending and the receiving end of the connection.
// The latency returned is updated continuously and is half the round trip time (RTT).
func (conn *Conn) Latency() time.Duration {
	return time.Duration(atomic.LoadUint32(&conn.latency))
}

// sendPing pings the connection, updating the latency of the Conn if successful.
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
// of the datagram are handled. if not, an error is returned.
func (conn *Conn) receiveDatagram(b *bytes.Buffer) error {
	sequenceNumber, err := readUint24(b)
	if err != nil {
		return fmt.Errorf("error reading datagram sequence number: %v", err)
	}
	// Add this sequence number to the received datagrams so we ACK it.
	conn.datagramsReceived.Store(append(conn.datagramsReceived.Load().([]uint24), sequenceNumber))

	lowestBefore := conn.datagramQueue.lowest
	if !conn.datagramQueue.put(sequenceNumber) {
		return fmt.Errorf("error handing datagram: datagram already received")
	}

	if conn.datagramQueue.lowest == lowestBefore {
		// We couldn't take any datagram out of the receive queue, meaning we are missing a datagram. We
		// increment the counter, and if it exceeds the threshold we send a NACK to request again.
		conn.missingDatagramTimes++
		if conn.missingDatagramTimes >= resendRequestThreshold {
			if err := conn.sendNACK(conn.datagramQueue.missing()); err != nil {
				return fmt.Errorf("error sending NACK to request datagrams: %v", err)
			}
		}
	} else {
		conn.missingDatagramTimes = 0
	}
	if conn.datagramQueue.WindowSize() > maxWindowSize && !conn.client {
		return fmt.Errorf("datagram receive queue window size is too big")
	}

	for b.Len() > 0 {
		if err := conn.readPacket.read(b); err != nil {
			return fmt.Errorf("error decoding datagram packet: %v", err)
		}
		if conn.readPacket.split {
			if err := conn.handleSplitPacket(conn.readPacket); err != nil {
				return fmt.Errorf("error receiving split packet: %v", err)
			}
			continue
		}
		if err := conn.receivePacket(conn.readPacket); err != nil {
			return fmt.Errorf("error receiving packet: %v", err)
		}
	}
	return nil
}

// receivePacket handles the receiving of a packet. It puts the packet in the queue and takes out all packets
// that were obtainable after that, and handles them.
func (conn *Conn) receivePacket(packet *packet) error {
	// Update the last time we received a packet so that the connection doesn't time out.
	conn.lastPacketTime.Store(time.Now())

	if packet.reliability != reliabilityReliableOrdered {
		// If it isn't a reliable ordered packet, handle it immediately.
		return conn.handlePacket(packet.content)
	}
	if !conn.packetQueue.put(packet.orderIndex, packet.content) {
		// An ordered packet arrived twice.
		return nil
	}
	if conn.packetQueue.WindowSize() > maxWindowSize && !conn.client {
		return fmt.Errorf("packet queue window size is too big")
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
		if conn.connected {
			return nil
		}
		return conn.handleConnectionRequest(buffer)
	case message.IDConnectionRequestAccepted:
		if conn.connected {
			return nil
		}
		return conn.handleConnectionRequestAccepted(buffer)
	case message.IDNewIncomingConnection:
		if conn.connected {
			return nil
		}
		conn.finishSequence()
		conn.connected = true
	case message.IDConnectedPing:
		return conn.handleConnectedPing(buffer)
	case message.IDConnectedPong:
		return conn.handleConnectedPong(buffer)
	case message.IDDisconnectNotification:
		return conn.Close()
	case 04:
		// This packet doesn't matter to us: We just ignore it but do put it in a switch case so that it isn't
		// forwarded like a normal packet.
		return nil
	default:
		if err := buffer.UnreadByte(); err != nil {
			return fmt.Errorf("error unreading custom packet ID: %v", err)
		}
		// Insert the packet contents the packet queue could release in the channel so that Conn.Read() can
		// get a hold of them, but always first try to escape if the connection was closed.
		select {
		case <-conn.closeCtx.Done():
		case conn.packetChan <- buffer:
		}
		return nil
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
	now := timestamp()
	if packet.ClientTimestamp > now {
		return fmt.Errorf("error measuring latency: ping timestamp is in the future")
	}
	// We don't actually use the ConnectedPong to measure latency. It is too unreliable and doesn't give a
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

	conn.finishSequence()
	conn.connected = true
	return err
}

// handleSplitPacket handles a passed split packet. If it is the last split packet of its sequence, it will
// continue handling the full packet as it otherwise would.
// An error is returned if the packet was not valid.
func (conn *Conn) handleSplitPacket(p *packet) error {
	const maxSplitCount = 256
	if p.splitCount > maxSplitCount && !conn.client || len(conn.splits) > 16 {
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
			// We haven't yet received all split fragments, so we cannot put the packets together yet.
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
	ack := &acknowledgement{}

	for {
		ack.packets = packets
		if len(ack.packets) > 4096 {
			ack.packets = packets[:4096]
			packets = packets[4096:]
		} else {
			packets = packets[:0]
		}
		buf.WriteByte(bitflag | bitFlagDatagram)
		if err := ack.write(buf); err != nil {
			return fmt.Errorf("error encoding ACK packet: %v", err)
		}
		if _, err := conn.conn.WriteTo(buf.Bytes(), conn.addr); err != nil {
			return fmt.Errorf("error sending ACK packet: %v", err)
		}
		if len(packets) == 0 {
			return nil
		}
	}
}

// handleACK handles an acknowledgement packet from the other end of the connection. These mean that a
// datagram was successfully received by the other end.
func (conn *Conn) handleACK(b *bytes.Buffer) error {
	conn.writeLock.Lock()
	defer conn.writeLock.Unlock()

	ack := &acknowledgement{}
	if err := ack.read(b); err != nil {
		return fmt.Errorf("error reading ACK: %v", err)
	}
	for _, sequenceNumber := range ack.packets {
		// Take out all stored packets from the recovery queue.
		p, ok := conn.recoveryQueue.take(sequenceNumber)
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
	conn.writeLock.Lock()
	defer conn.writeLock.Unlock()

	nack := &acknowledgement{}
	if err := nack.read(b); err != nil {
		return fmt.Errorf("error reading NACK: %v", err)
	}
	return conn.resend(nack.packets)
}

// resend sends all datagrams currently in the recovery queue with the sequence numbers passed.
func (conn *Conn) resend(sequenceNumbers []uint24) (err error) {
	for _, sequenceNumber := range sequenceNumbers {
		packet, ok := conn.recoveryQueue.takeWithoutDelayAdd(sequenceNumber)
		if !ok {
			// We could not resend this datagram. Maybe it was already resent before at the request of the
			// client. This is generally expected so we just continue.
			continue
		}

		// We first write a new datagram header using a new send sequence number that we find.
		if err := conn.writeBuf.WriteByte(bitFlagDatagram); err != nil {
			return fmt.Errorf("error writing recovered datagram header: %v", err)
		}
		newSeqNum := conn.sendSequenceNumber
		conn.sendSequenceNumber++
		writeUint24(conn.writeBuf, newSeqNum)
		if err := packet.write(conn.writeBuf); err != nil {
			return fmt.Errorf("error writing recovered packet to buffer: %v", err)
		}

		// We then send the packet to the connection.
		if _, err := conn.conn.WriteTo(conn.writeBuf.Bytes(), conn.addr); err != nil {
			return fmt.Errorf("error sending packet to addr %v: %v", conn.addr, err)
		}
		// We then re-add the packet to the recovery queue in case the new one gets lost too, in which case
		// we need to resend it again.
		conn.recoveryQueue.put(newSeqNum, packet)
		conn.writeBuf.Reset()
	}
	return nil
}

// requestConnection requests the connection from the server, provided this connection operates as a client.
// An error occurs if the request was not successful.
func (conn *Conn) requestConnection() error {
	b := bytes.NewBuffer(nil)
	(&message.ConnectionRequest{ClientGUID: conn.id, RequestTimestamp: timestamp()}).Write(b)
	_, err := conn.Write(b.Bytes())
	return err
}
