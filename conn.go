package raknet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// MinecraftProtocol is the current default Minecraft RakNet protocol version. This is Minecraft specific.
	// For the default RakNet, use OfficialProtocol.
	// MinecraftProtocol is the default in go-raknet.
	MinecraftProtocol byte = 9
	// OfficialProtocol is the protocol version of the official open source RakNet library.
	OfficialProtocol byte = 6

	// connTimeout is the timeout after which a conn times out, if it hasn't received a packet for that
	// duration.
	connTimeout = time.Second * 7
	// resendRequestThreshold is the amount of datagrams that must be received before datagrams that were
	// missing earlier will be requested to be resent.
	resendRequestThreshold = 10
	// tickInterval is the interval at which the connection sends an ACK containing the are sent.
	tickInterval = time.Second / 100
	// pingInterval is the interval in seconds at which a ping is sent to the other end of the connection.
	pingInterval = time.Second * 4
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
	conn net.PacketConn
	addr net.Addr

	writeLock   sync.Mutex
	writeBuffer *bytes.Buffer

	readPacket *packet

	sendSequenceNumber uint24
	sendOrderIndex     uint24
	sendMessageIndex   uint24
	sendSplitID        uint32

	// finishedSequence is a channel that has a value submitted to it once the connection has finished the
	// RakNet connection sequence.
	finishedSequence chan bool

	// id is the random client GUID of the client. It is different each time a client connects to to a server.
	id int64
	// mtuSize is the MTU size of the connection. Packets longer than this size must be split into fragments
	// for them to arrive at the client without losing bytes.
	mtuSize int16

	// latency is the last measured latency between both ends of the connection. Note that this latency is
	// not the round-trip time, but half of that.
	latency int

	// splits is a map of slices indexed by split IDs. The length of each of the slices is equal to the split
	// count, and packets are positioned in that slice indexed by the split index.
	splits map[uint16][][]byte

	// datagramRecvQueue is an ordered queue used to track which datagrams were received and which datagrams
	// were missing, so that we can send NACKs to request missing datagrams.
	datagramRecvQueue *orderedQueue
	// datagramsReceived is a slice containing sequence numbers of datagrams that were received over the last
	// 3 seconds. When ticked, all of these packets are sent in an ACK and the slice is cleared.
	datagramsReceived atomic.Value
	// missingDatagramTimes is the times that a datagram was received, but a previous datagram was not.
	missingDatagramTimes int

	// packetQueue is an ordered queue containing packets indexed by their order index.
	packetQueue *orderedQueue
	// packetChan is a channel containing content of packets that were fully processed. Calling Conn.Read()
	// consumes a value from this channel.
	packetChan chan *bytes.Buffer
	// lastPacketTime is the last time a packet was received. It is used to measure the time until the
	// connection times out.
	lastPacketTime atomic.Value

	// recoveryQueue is a queue filled with packets that were sent with a given datagram sequence number.
	recoveryQueue *orderedQueue

	// close gets sent values when the connection is closed.
	close chan bool

	// readDeadline is a channel that receives a time.Time after a specific time. It is used to listen for
	// timeouts in Read after calling SetReadDeadline.
	readDeadline <-chan time.Time
}

// newConn constructs a new connection specifically dedicated to the address passed.
func newConn(conn net.PacketConn, addr net.Addr, mtuSize int16, id int64) *Conn {
	if mtuSize < 500 {
		mtuSize = 500
	}
	c := &Conn{
		addr:              addr,
		conn:              conn,
		mtuSize:           mtuSize,
		id:                id,
		finishedSequence:  make(chan bool),
		splits:            make(map[uint16][][]byte),
		datagramRecvQueue: newOrderedQueue(),
		packetQueue:       newOrderedQueue(),
		recoveryQueue:     newOrderedQueue(),
		close:             make(chan bool, 1),
		packetChan:        make(chan *bytes.Buffer),
		writeBuffer:       bytes.NewBuffer(nil),
		readPacket:        &packet{},
	}
	c.lastPacketTime.Store(time.Now())
	c.datagramsReceived.Store([]uint24{})
	go func() {
		ticker := time.NewTicker(tickInterval)
		pingTicker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		defer pingTicker.Stop()
		for {
			select {
			case <-pingTicker.C:
				// We send a connected ping every four seconds to calculate the latency and let the other side
				// know we haven't timed out.
				packet := &connectedPing{PingTimestamp: timestamp()}
				b := bytes.NewBuffer([]byte{idConnectedPing})
				_ = binary.Write(b, binary.BigEndian, packet)
				if _, err := c.Write(b.Bytes()); err != nil {
					return
				}
			case t := <-ticker.C:
				received := c.datagramsReceived.Load().([]uint24)
				if len(received) > 0 {
					// Write an ACK packet to the connection containing all datagram sequence numbers that we
					// received since the last tick.
					if err := c.sendACK(received...); err != nil {
						return
					}
					c.datagramsReceived.Store(received[:0])
				}

				// After that, we check if the other end has actually timed out. If so, we close the conn, as
				// it is likely the client was disconnected.
				if t.Sub(c.lastPacketTime.Load().(time.Time)) > connTimeout {
					// If the timeout was long enough, we close the conn.
					_ = c.Close()
				}

			case <-c.close:
				// Put a new boolean in the close channel to make sure other receivers also get it.
				c.close <- true
				return
			}
		}
	}()

	return c
}

// Write writes a buffer b over the RakNet connection. The amount of bytes written n is always equal to the
// length of the bytes written if the write was successful. If not, an error is returned and n is 0.
// Write may be called simultaneously from multiple goroutines, but will write one by one.
func (conn *Conn) Write(b []byte) (n int, err error) {
	conn.writeLock.Lock()
	defer conn.writeLock.Unlock()

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

		if err := conn.writeBuffer.WriteByte(bitFlagValid); err != nil {
			return 0, fmt.Errorf("error writing datagram header: %v", err)
		}
		if err := writeUint24(conn.writeBuffer, sequenceNumber); err != nil {
			return 0, fmt.Errorf("error writing datagram sequence number: %v", err)
		}
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
		if err := packet.write(conn.writeBuffer); err != nil {
			return 0, fmt.Errorf("error writing packet to buffer: %v", err)
		}
		// We then send the packet to the connection.
		if _, err := conn.conn.WriteTo(conn.writeBuffer.Bytes(), conn.addr); err != nil {
			return 0, fmt.Errorf("error sending packet to addr %v: %v", conn.addr, err)
		}
		// We reset the buffer so that we can re-use it for each fragment created when splitting the packet.
		conn.writeBuffer.Reset()

		// Finally we add the packet to the recovery queue.
		_ = conn.recoveryQueue.put(sequenceNumber, packet)
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
	case <-conn.close:
		conn.close <- true
		return 0, errors.New(errConnectionClosed)
	case <-conn.readDeadline:
		return 0, errors.New(errReadTimeout)
	}
}

// Close closes the connection. All blocking Read or Write actions are cancelled and will return an error.
func (conn *Conn) Close() error {
	if len(conn.close) != 0 {
		// The connection was already closed, don't do anything.
		return nil
	}
	conn.close <- true
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
	conn.readDeadline = time.After(t.Sub(time.Now()))
	return nil
}

// SetWriteDeadline has no behaviour. It is merely there to satisfy the net.Conn interface.
func (conn *Conn) SetWriteDeadline(t time.Time) error {
	return nil
}

// SetDeadline sets the deadline of the connection for both Read and Write. SetDeadline is equivalent to
// calling both SetReadDeadline and SetWriteDeadline.
func (conn *Conn) SetDeadline(t time.Time) error {
	return conn.SetReadDeadline(t)
}

// Latency returns the last measured latency between both ends of the connection in milliseconds. The latency
// is updated every 4 seconds. The latency returned is the time it takes to send one packet from one end to
// the other end of the connection. It is not the round-trip time.
func (conn *Conn) Latency() int {
	return conn.latency
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
	if headerFlags&bitFlagValid == 0 {
		// Close the connection if a non-datagram packet was received. This is probably an offline message.
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
	if err := conn.datagramRecvQueue.put(sequenceNumber, true); err != nil {
		return fmt.Errorf("error handing datagram: datagram already received")
	}
	conn.datagramsReceived.Store(append(conn.datagramsReceived.Load().([]uint24), sequenceNumber))
	if len(conn.datagramRecvQueue.takeOut()) == 0 {
		// We couldn't take any datagram out of the receive queue, meaning we are missing a datagram. We
		// increment the counter, and if it exceeds the threshold we send a NACK to request again.
		conn.missingDatagramTimes++
		if conn.missingDatagramTimes >= resendRequestThreshold {
			if err := conn.sendNACK(conn.datagramRecvQueue.missing()...); err != nil {
				return fmt.Errorf("error sending NACK to request datagrams: %v", err)
			}
			// Take all 'datagrams' that were put in by the datagramRecvQueue.missing() call out of the queue,
			// as datagrams that we will receive again will have a different sequence number.
			conn.datagramRecvQueue.takeOut()
		}
	} else {
		conn.missingDatagramTimes = 0
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
	if packet.reliability != reliabilityReliableOrdered {
		// If it isn't a reliable ordered packet, handle it immediately.
		return conn.handlePacket(packet.content)
	}
	if err := conn.packetQueue.put(packet.orderIndex, packet.content); err != nil {
		if packet.orderIndex == 0 {
			return conn.handlePacket(packet.content)
		}
		// Don't return these errors. We'll have a packet that was sent either multiple times, arrived
		// multiple times or something else. These aren't critical errors.
		return nil
	}
	for _, packetContent := range conn.packetQueue.takeOut() {
		if err := conn.handlePacket(packetContent.([]byte)); err != nil {
			return fmt.Errorf("error handling packet: %v", err)
		}
	}
	return nil
}

// handlePacket handles a packet serialised in byte slice b. If not successful, an error is returned. If the
// packet was not handled by RakNet, it is sent to the packet channel.
func (conn *Conn) handlePacket(b []byte) error {
	buffer := bytes.NewBuffer(b)
	header, err := buffer.ReadByte()
	if err != nil {
		return fmt.Errorf("error reading packet ID: %v", err)
	}

	// Update the last time we received a packet so that the connection doesn't time out.
	conn.lastPacketTime.Store(time.Now())

	switch header {
	case idConnectionRequest:
		return conn.handleConnectionRequest(buffer)
	case idConnectionRequestAccepted:
		return conn.handleConnectionRequestAccepted(buffer)
	case idNewIncomingConnection:
		conn.finishedSequence <- true
	case idConnectedPing:
		return conn.handleConnectedPing(buffer)
	case idConnectedPong:
		return conn.handleConnectedPong(buffer)
	case idDisconnectNotification:
		return conn.Close()
	default:
		if err := buffer.UnreadByte(); err != nil {
			return fmt.Errorf("error unreading custom packet ID: %v", err)
		}
		// Insert the packet contents the packet queue could release in the channel so that Conn.Read() can
		// get a hold of them.
		conn.packetChan <- buffer
	}
	return nil
}

// handleConnectedPing handles a connected ping packet inside of buffer b. An error is returned if the packet
// was invalid.
func (conn *Conn) handleConnectedPing(b *bytes.Buffer) error {
	packet := &connectedPing{}
	if err := binary.Read(b, binary.BigEndian, packet); err != nil {
		return fmt.Errorf("error reading connected ping: %v", err)
	}
	b.Reset()

	// Respond with a connected pong that has the ping timestamp found in the connected ping, and our own
	// timestamp for the pong timestamp.
	response := &connectedPong{PingTimestamp: packet.PingTimestamp, PongTimestamp: timestamp()}
	if err := b.WriteByte(idConnectedPong); err != nil {
		return fmt.Errorf("error writing connected pong ID: %v", err)
	}
	if err := binary.Write(b, binary.BigEndian, response); err != nil {
		return fmt.Errorf("error writing connected pong: %v", err)
	}
	if _, err := conn.Write(b.Bytes()); err != nil {
		return fmt.Errorf("error sending connected pong: %v", err)
	}
	return nil
}

// handleConnectedPong handles a connected pong packet inside of buffer b. An error is returned if the packet
// was invalid.
func (conn *Conn) handleConnectedPong(b *bytes.Buffer) error {
	packet := &connectedPong{}
	if err := binary.Read(b, binary.BigEndian, packet); err != nil {
		return fmt.Errorf("error reading connected pong: %v", err)
	}
	now := timestamp()
	if packet.PingTimestamp > now {
		return fmt.Errorf("error measuring latency: ping timestamp is in the future")
	}
	// We measure the latency for a single packet from one end to another, not the round-trip time, so we
	// divide the total time by 2.
	conn.latency = int(now-packet.PingTimestamp) / 2

	return nil
}

// handleConnectionRequest handles a connection request packet inside of buffer b. An error is returned if the
// packet was invalid.
func (conn *Conn) handleConnectionRequest(b *bytes.Buffer) error {
	packet := &connectionRequest{}
	if err := binary.Read(b, binary.BigEndian, packet); err != nil {
		return fmt.Errorf("error reading connection request: %v", err)
	}
	b.Reset()

	if err := b.WriteByte(idConnectionRequestAccepted); err != nil {
		return fmt.Errorf("error writing connection request accepted ID: %v", err)
	}
	addr := rakAddr(*conn.addr.(*net.UDPAddr))
	data, err := (&addr).MarshalBinary()
	if err != nil {
		return fmt.Errorf("error encoding connection request accepted client address: %v", err)
	}
	if _, err := b.Write(data); err != nil {
		return fmt.Errorf("error writing connection request accepted client address: %v", err)
	}
	_ = binary.Write(b, binary.BigEndian, int16(0))
	for i := 0; i < 20; i++ {
		// The middle of the connection request accepted packet has 20 system addresses. We write these
		// separately.
		var addr *rakAddr
		encodedAddr, err := addr.MarshalBinary()
		if err != nil {
			return fmt.Errorf("error encoding connection request accepted system address: %v", err)
		}
		if _, err := b.Write(encodedAddr); err != nil {
			return fmt.Errorf("error writing connection request accepted system address: %v", err)
		}
	}
	response := &connectionRequestAccepted{RequestTimestamp: packet.RequestTimestamp, AcceptedTimestamp: timestamp()}
	if err := binary.Write(b, binary.BigEndian, response); err != nil {
		return fmt.Errorf("error writing connection request accepted: %v", err)
	}
	if _, err := conn.Write(b.Bytes()); err != nil {
		return fmt.Errorf("error sending connection request accepted: %v", err)
	}

	return nil
}

// handleConnectionRequestAccepted handles a serialised connection request accepted packet in b, and returns
// an error if not successful.
func (conn *Conn) handleConnectionRequestAccepted(b *bytes.Buffer) error {
	b.Reset()

	if err := b.WriteByte(idNewIncomingConnection); err != nil {
		return fmt.Errorf("error writing new incoming connection ID: %v", err)
	}
	addr := rakAddr(*conn.addr.(*net.UDPAddr))
	data, err := (&addr).MarshalBinary()
	if err != nil {
		return fmt.Errorf("error encoding new incoming ocnnection server address: %v", err)
	}
	if _, err := b.Write(data); err != nil {
		return fmt.Errorf("error writing new incoming connection server address: %v", err)
	}
	for i := 0; i < 20; i++ {
		// The middle of the connection request accepted packet has 20 system addresses. We write these
		// separately.
		var addr *rakAddr
		encodedAddr, err := addr.MarshalBinary()
		if err != nil {
			return fmt.Errorf("error encoding new incoming connection system address: %v", err)
		}
		if _, err := b.Write(encodedAddr); err != nil {
			return fmt.Errorf("error writing new incoming connection system address: %v", err)
		}
	}
	// We fill out nonsense timestamps as RakNet doesn't REALLY care about these.
	response := &newIncomingConnection{RequestTimestamp: timestamp(), AcceptedTimestamp: timestamp()}
	if err := binary.Write(b, binary.BigEndian, response); err != nil {
		return fmt.Errorf("error writing new incoming connection: %v", err)
	}
	if _, err := conn.Write(b.Bytes()); err != nil {
		return fmt.Errorf("error sending new incoming connection: %v", err)
	}

	conn.finishedSequence <- true
	return nil
}

// handleSplitPacket handles a passed split packet. If it is the last split packet of its sequence, it will
// continue handling the full packet as it otherwise would.
// An error is returned if the packet was not valid.
func (conn *Conn) handleSplitPacket(p *packet) error {
	m, ok := conn.splits[p.splitID]
	if !ok {
		m = make([][]byte, p.splitCount)
		conn.splits[p.splitID] = m
	}
	if p.splitIndex > uint32(len(m)-1) {
		// The split index was either negative or was bigger than the slice size, meaning the packet is
		// invalid.
		return fmt.Errorf("error handing split packet: split ID %v is out of range (0 - %v)", p.splitID, len(m)-1)
	}
	m[p.splitIndex] = p.content

	for _, splitPacket := range m {
		if len(splitPacket) == 0 {
			// We haven't yet received all split fragments, so we cannot put the packets together yet.
			return nil
		}
	}

	totalSize := 0
	for _, splitPacket := range m {
		// First we calculate the total size required to hold the content of the combined content.
		totalSize += len(splitPacket)
	}
	fullContent := make([]byte, totalSize)
	currentOffset := 0
	for _, splitPacket := range m {
		// We finally copy the packet into our new full content slice and make sure it is copied at the
		// correct offset.
		contentLength := len(splitPacket)
		if n := copy(fullContent[currentOffset:], splitPacket); n != contentLength {
			panic(fmt.Sprintf("invalid length full split packet content byte slice produced: should have copied %v, but only copied %v", contentLength, n))
		}
		currentOffset += contentLength
	}
	delete(conn.splits, p.splitID)

	p.content = fullContent
	return conn.receivePacket(p)
}

// sendACK sends an acknowledgement packet containing the packet sequence numbers passed. If not successful,
// an error is returned.
func (conn *Conn) sendACK(packets ...uint24) error {
	ack := &acknowledgement{packets: packets}
	buffer := bytes.NewBuffer([]byte{bitFlagACK | bitFlagValid})
	if err := ack.write(buffer); err != nil {
		return fmt.Errorf("error encoding ACK packet: %v", err)
	}
	if _, err := conn.conn.WriteTo(buffer.Bytes(), conn.addr); err != nil {
		return fmt.Errorf("error sending ACK packet: %v", err)
	}
	return nil
}

// sendNACK sends an acknowledgement packet containing the packet sequence numbers passed. If not successful,
// an error is returned.
func (conn *Conn) sendNACK(packets ...uint24) error {
	ack := &acknowledgement{packets: packets}
	buffer := bytes.NewBuffer([]byte{bitFlagNACK | bitFlagValid})
	if err := ack.write(buffer); err != nil {
		return fmt.Errorf("error encoding NACK packet: %v", err)
	}
	if _, err := conn.conn.WriteTo(buffer.Bytes(), conn.addr); err != nil {
		return fmt.Errorf("error sending NACK packet: %v", err)
	}
	return nil
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
			p.(*packet).content = nil
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
	for _, sequenceNumber := range nack.packets {
		val, ok := conn.recoveryQueue.take(sequenceNumber)
		if !ok {
			return fmt.Errorf("error recovering NACK for sequence number %v", sequenceNumber)
		}
		packet := val.(*packet)

		// We first write a new datagram header using a new send sequence number that we find.
		if err := conn.writeBuffer.WriteByte(bitFlagValid); err != nil {
			return fmt.Errorf("error writing recovered datagram header: %v", err)
		}
		sequenceNumber := conn.sendSequenceNumber
		conn.sendSequenceNumber++
		if err := writeUint24(conn.writeBuffer, sequenceNumber); err != nil {
			return fmt.Errorf("error writing recovered datagram sequence number: %v", err)
		}

		if err := packet.write(conn.writeBuffer); err != nil {
			return fmt.Errorf("error writing recovered packet to buffer: %v", err)
		}
		// We then send the packet to the connection.
		if _, err := conn.conn.WriteTo(conn.writeBuffer.Bytes(), conn.addr); err != nil {
			return fmt.Errorf("error sending recovered packet to addr %v: %v", conn.addr, err)
		}
		// We then re-add the packet to the recovery queue in case the new one gets lost too, in which case
		// we need to resend it again.
		_ = conn.recoveryQueue.put(sequenceNumber, packet)
		conn.writeBuffer.Reset()
	}
	return nil
}

// requestConnection requests the connection from the server, provided this connection operates as a client.
// An error occurs if the request was not successful.
func (conn *Conn) requestConnection() error {
	b := bytes.NewBuffer([]byte{idConnectionRequest})
	packet := &connectionRequest{ClientGUID: conn.id, RequestTimestamp: timestamp()}
	if err := binary.Write(b, binary.BigEndian, packet); err != nil {
		return fmt.Errorf("error writing connection request: %v", err)
	}
	if _, err := conn.Write(b.Bytes()); err != nil {
		return fmt.Errorf("error sending connection request: %v", err)
	}
	return nil
}
