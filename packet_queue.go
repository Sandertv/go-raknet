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

// put puts a value at the index passed. If the index was already occupied once, false is returned.
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

// fetch attempts to take out as many values from the ordered queue as possible. Upon encountering an index
// that has no value yet, the function returns all values that it did find and takes them out.
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
