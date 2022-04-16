package raknet

import (
	"time"
)

// datagramWindow is a queue for incoming datagrams.
type datagramWindow struct {
	lowest, highest uint24
	queue           map[uint24]time.Time
}

// newDatagramWindow returns a new initialised datagram window.
func newDatagramWindow() *datagramWindow {
	return &datagramWindow{queue: make(map[uint24]time.Time)}
}

// new checks if the index passed is new to the datagramWindow.
func (win *datagramWindow) new(index uint24) bool {
	if index < win.lowest {
		return true
	}
	_, ok := win.queue[index]
	return !ok
}

// add puts an index in the window.
func (win *datagramWindow) add(index uint24) {
	if index >= win.highest {
		win.highest = index + 1
	}
	win.queue[index] = time.Now()
}

// shift attempts to delete as many indices from the queue as possible, increasing the lowest index if and when
// possible.
func (win *datagramWindow) shift() (n int) {
	var index uint24
	for index = win.lowest; index < win.highest; index++ {
		if _, ok := win.queue[index]; !ok {
			break
		}
		delete(win.queue, index)
		n++
	}
	win.lowest = index
	return n
}

// missing returns a slice of all indices in the datagram queue that weren't set using add while within the
// window of lowest and highest index. The queue is shifted after this call.
func (win *datagramWindow) missing(since time.Duration) (indices []uint24) {
	var (
		missing = false
	)
	for index := int(win.highest) - 1; index >= int(win.lowest); index-- {
		i := uint24(index)
		t, ok := win.queue[i]
		if ok {
			if time.Since(t) >= since {
				// All packets before this one took too long to arrive, so we mark them as missing.
				missing = true
			}
			continue
		}
		if missing {
			indices = append(indices, i)
			win.queue[i] = time.Time{}
		}
	}
	win.shift()
	return indices
}

// size returns the size of the datagramWindow.
func (win *datagramWindow) size() uint24 {
	return win.highest - win.lowest
}
