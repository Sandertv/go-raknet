package raknet

import (
	"fmt"
)

// orderedQueue is a queue of byte slices that are taken out in an ordered way. No byte slice may be taken out
// that has been inserted with an index if all indices below that aren't also taken out.
// orderedQueue is not safe for concurrent use.
type orderedQueue struct {
	queue        map[uint24]interface{}
	lowestIndex  uint24
	highestIndex uint24
}

// newOrderedQueue returns a new initialised ordered queue.
func newOrderedQueue() *orderedQueue {
	return &orderedQueue{queue: make(map[uint24]interface{})}
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
	return nil
}

// take fetches a value from the index passed and removes the value from the queue. If the value was found, ok
// is true.
func (queue *orderedQueue) take(index uint24) (val interface{}, ok bool) {
	val, ok = queue.queue[index]
	delete(queue.queue, index)
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
		if value == nil {
			// Value was set by calling queue.missing(). This was a substitute value.
			delete(queue.queue, index)
			continue
		}
		values = append(values, value)
		delete(queue.queue, index)
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
