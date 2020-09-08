package raknet

// datagramQueue is a queue for incoming datagrams.
type datagramQueue struct {
	lowest  uint24
	highest uint24
	queue   map[uint24]struct{}
}

// newDatagramQueue returns a new initialised datagram queue.
func newDatagramQueue() *datagramQueue {
	return &datagramQueue{queue: make(map[uint24]struct{})}
}

// put puts an index in the queue. If the index was already occupied once, false is returned.
func (queue *datagramQueue) put(index uint24) bool {
	if index < queue.lowest {
		return false
	}
	if _, ok := queue.queue[index]; ok {
		return false
	}
	if index >= queue.highest {
		queue.highest = index + 1
	}
	queue.queue[index] = struct{}{}
	return true
}

// clear attempts to clear as many indices from the queue as possible, increasing the lowest index if and when
// possible.
func (queue *datagramQueue) clear() {
	var index uint24
	for index = queue.lowest; index < queue.highest; index++ {
		if _, ok := queue.queue[index]; !ok {
			break
		}
		delete(queue.queue, index)
	}
	queue.lowest = index
}

// missing returns a slice of all indices in the datagram queue that weren't set using put while within the
// window of lowest and highest index. The queue is cleared after this call.
func (queue *datagramQueue) missing() (indices []uint24) {
	for index := queue.lowest; index < queue.highest; index++ {
		if _, ok := queue.queue[index]; !ok {
			indices = append(indices, index)
			queue.queue[index] = struct{}{}
		}
	}
	queue.clear()
	return indices
}

// WindowSize returns the size of the window held by the datagram queue.
func (queue *datagramQueue) WindowSize() uint24 {
	return queue.highest - queue.lowest
}
