package raknet

import (
	"time"
)

const delayRecordCount = 40

// recoveryQueue is a queue of packets, used to recover datagrams if the other end of the connection ended up
// not having them.
type recoveryQueue struct {
	queue      map[uint24]*packet
	timestamps map[uint24]time.Time

	ptr    int
	delays []time.Duration
}

// newRecoveryQueue returns a new initialised recovery queue.
func newRecoveryQueue() *recoveryQueue {
	return &recoveryQueue{queue: make(map[uint24]*packet), timestamps: make(map[uint24]time.Time), delays: make([]time.Duration, delayRecordCount)}
}

// put puts a value at the index passed.
func (queue *recoveryQueue) put(index uint24, value *packet) {
	queue.queue[index] = value
	queue.timestamps[index] = time.Now()
}

// take fetches a value from the index passed and removes the value from the queue. If the value was found, ok
// is true.
func (queue *recoveryQueue) take(index uint24) (val *packet, ok bool) {
	val, ok = queue.queue[index]
	if ok {
		delete(queue.queue, index)
		queue.delays[queue.ptr] = time.Since(queue.timestamps[index])
		queue.ptr++
		if queue.ptr == delayRecordCount {
			queue.ptr = 0
		}
		delete(queue.timestamps, index)
	}
	return
}

// takeWithoutDelayAdd has the same functionality as take, but does not update the time it took for the
// datagram to arrive.
func (queue *recoveryQueue) takeWithoutDelayAdd(index uint24) (val *packet, ok bool) {
	val, ok = queue.queue[index]
	if ok {
		delete(queue.queue, index)
		delete(queue.timestamps, index)
	}
	return
}

// Timestamp returns the a timestamp of the time that a packet with the sequence number passed arrived at in
// the recovery queue. It panics if the sequence number doesn't exist.
func (queue *recoveryQueue) Timestamp(sequenceNumber uint24) time.Time {
	return queue.timestamps[sequenceNumber]
}

// AvgDelay returns the average delay between the putting of the value into the recovery queue and the taking
// out of it again. It is measured over the last delayRecordCount values put in.
func (queue *recoveryQueue) AvgDelay() time.Duration {
	var average, records time.Duration
	for _, delay := range queue.delays {
		if delay == 0 {
			break
		}
		average += delay
		records++
	}
	if records == 0 {
		// No records yet, generally should not happen. Just return a reasonable amount of time.
		return time.Millisecond * 50
	}
	return average / records
}
