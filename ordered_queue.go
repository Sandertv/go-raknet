package raknet

import (
	"fmt"
	"time"
)

const DelayRecordCount = 40

// orderedQueue is a queue of byte slices that are taken out in an ordered way. No byte slice may be taken out
// that has been inserted with an index if all indices below that aren't also taken out.
// orderedQueue is not safe for concurrent use.
type orderedQueue struct {
	queue        map[uint24]interface{}
	timestamps   map[uint24]time.Time
	lowestIndex  uint24
	highestIndex uint24
	lastClean    time.Time

	ptr    int
	delays []time.Duration
}

// newOrderedQueue returns a new initialised ordered queue.
func newOrderedQueue() *orderedQueue {
	return &orderedQueue{queue: make(map[uint24]interface{}), timestamps: make(map[uint24]time.Time), delays: make([]time.Duration, DelayRecordCount)}
}

// put puts a value at the index passed. If the index was already occupied once, an error is returned.
func (queue *orderedQueue) put(index uint24, value interface{}) error {
	if index < queue.lowestIndex {
		return fmt.Errorf("cannot set value at index %v: already taken out", index)
	}
	if _, ok := queue.queue[index]; ok {
		return fmt.Errorf("cannot set value at index %v: already has a value", index)
	}
	if index+1 > queue.highestIndex {
		queue.highestIndex = index + 1
	}
	queue.queue[index] = value
	queue.timestamps[index] = time.Now()
	return nil
}

// take fetches a value from the index passed and removes the value from the queue. If the value was found, ok
// is true.
func (queue *orderedQueue) take(index uint24) (val interface{}, ok bool) {
	val, ok = queue.queue[index]
	if ok {
		delete(queue.queue, index)
		queue.delays[queue.ptr] = time.Now().Sub(queue.timestamps[index])
		queue.ptr++
		if queue.ptr == DelayRecordCount {
			queue.ptr = 0
		}
		delete(queue.timestamps, index)
	}
	return
}

// takeWithoutDelayAdd has the same functionality as take, but does not update the time it took for the
// datagram to arrive.
func (queue *orderedQueue) takeWithoutDelayAdd(index uint24) (val interface{}, ok bool) {
	val, ok = queue.queue[index]
	if ok {
		delete(queue.queue, index)
		delete(queue.timestamps, index)
	}
	return
}

// takeOut attempts to take out as many values from the ordered queue as possible. Upon encountering an index
// that has no value yet, the function returns all values that it did find and takes them out.
func (queue *orderedQueue) takeOut() (values []interface{}) {
	var index uint24
	for index = queue.lowestIndex; index < queue.highestIndex; index++ {
		value, ok := queue.queue[index]
		if !ok {
			break
		}
		delete(queue.queue, index)
		delete(queue.timestamps, index)
		if value == nil {
			// Value was set by calling queue.missing(). This was a substitute value.
			continue
		}
		values = append(values, value)
	}
	queue.lowestIndex = index
	return
}

// missing returns a slice of all indices in the ordered queue that do not have a value in them yet. Upon
// returning, it also treats these indices as if they were filled out, meaning the next call to takeOut will
// be successful.
func (queue *orderedQueue) missing() (indices []uint24) {
	for index := queue.lowestIndex; index < queue.highestIndex; index++ {
		if _, ok := queue.queue[index]; !ok {
			indices = append(indices, index)
			queue.queue[index] = nil
		}
	}
	return
}

// Len returns the amount of values in the ordered queue.
func (queue *orderedQueue) Len() int {
	return len(queue.queue)
}

// Timestamp returns the a timestamp of the time that a packet with the sequence number passed arrived at in
// the recovery queue. It panics if the sequence number doesn't exist.
func (queue *orderedQueue) Timestamp(sequenceNumber uint24) time.Time {
	return queue.timestamps[sequenceNumber]
}

// AvgDelay returns the average delay between the putting of the value into the ordered queue and the taking
// out of it again. It is measured over the last DelayRecordCount values put in.
func (queue *orderedQueue) AvgDelay() time.Duration {
	var average, records time.Duration
	for _, delay := range queue.delays {
		if delay == 0 {
			break
		}
		average += delay
		records++
	}
	if records == 0 {
		// No records yet, we go with a rather high delay.
		return time.Second
	}
	return average / records
}
