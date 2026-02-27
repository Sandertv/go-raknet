package raknet

// packetQueue is an ordered queue for reliable ordered packets.
type packetQueue struct {
	lowest  uint24
	highest uint24
	queue   map[uint24][]byte
}

// newPacketQueue returns a new initialised ordered queue.
func newPacketQueue() *packetQueue {
	return &packetQueue{queue: make(map[uint24][]byte)}
}

// put puts a value at the index passed. If the index was already occupied
// once, false is returned.
func (queue *packetQueue) put(index uint24, packet []byte) bool {
	if index < queue.lowest {
		return false
	}
	if _, ok := queue.queue[index]; ok {
		return false
	}
	if index >= queue.highest {
		queue.highest = index + 1
	}
	queue.queue[index] = packet
	return true
}

// fetch attempts to take out as many values from the ordered queue as
// possible. Upon encountering an index that has no value yet, the function
// returns all values that it did find and takes them out.
func (queue *packetQueue) fetch() (packets [][]byte) {
	index := queue.lowest
	for index < queue.highest {
		packet, ok := queue.queue[index]
		if !ok {
			break
		}
		delete(queue.queue, index)
		packets = append(packets, packet)
		index++
	}
	queue.lowest = index
	return
}

// WindowSize returns the size of the window held by the packet queue.
func (queue *packetQueue) WindowSize() uint24 {
	return queue.highest - queue.lowest
}

// packetChannelQueue keeps a packet queue for each ordering channel.
type packetChannelQueue struct {
	channels map[byte]*packetQueue
}

// newPacketChannelQueue returns a new initialised per-channel ordered queue.
func newPacketChannelQueue() *packetChannelQueue {
	return &packetChannelQueue{channels: make(map[byte]*packetQueue)}
}

// put stores a packet in the queue for an ordering channel. It returns false
// if that packet index was already seen for the same channel.
func (queue *packetChannelQueue) put(channel byte, index uint24, packet []byte) bool {
	return queue.channel(channel).put(index, packet)
}

// fetch returns consecutive packets for the channel passed, starting at the
// current lowest index for that channel.
func (queue *packetChannelQueue) fetch(channel byte) [][]byte {
	ch, ok := queue.channels[channel]
	if !ok {
		return nil
	}
	return ch.fetch()
}

// WindowSize returns the size of the queue window for a specific channel.
func (queue *packetChannelQueue) WindowSize(channel byte) uint24 {
	ch, ok := queue.channels[channel]
	if !ok {
		return 0
	}
	return ch.WindowSize()
}

// TotalWindowSize returns the total size of all channel queue windows.
func (queue *packetChannelQueue) TotalWindowSize() (size uint24) {
	for _, ch := range queue.channels {
		size += ch.WindowSize()
	}
	return size
}

// WindowBounds returns the current low/high bounds of a channel queue.
func (queue *packetChannelQueue) WindowBounds(channel byte) (lowest, highest uint24) {
	ch, ok := queue.channels[channel]
	if !ok {
		return 0, 0
	}
	return ch.lowest, ch.highest
}

func (queue *packetChannelQueue) channel(channel byte) *packetQueue {
	if ch, ok := queue.channels[channel]; ok {
		return ch
	}
	ch := newPacketQueue()
	queue.channels[channel] = ch
	return ch
}
