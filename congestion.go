package raknet

import "math"

// congestionWindow limits reliable bytes in flight. Counts use encapsulated
// packet sizes and exclude the 4-byte datagram header. ACKs grow the window
// through slow start and then congestion avoidance. A NAK begins recovery by
// lowering the threshold; a timeout also resets the window to one MTU.
type congestionWindow struct {
	mtu       uint32
	window    float64
	threshold float64
	inFlight  uint32
	// nextBlock is the sequence number that starts the next congestion block: a
	// group of datagrams sent back to back, over which recovery happens at most
	// once so a burst of losses is treated as one event.
	nextBlock uint24
	// backedOff is set once the window has been reduced for the current block.
	backedOff bool
	// continuous reports whether more datagrams were waiting last tick. The
	// window only grows while the sender is actually pushing data.
	continuous bool
}

func newCongestionWindow(mtu uint16) congestionWindow {
	return congestionWindow{mtu: uint32(mtu), window: float64(mtu)}
}

// transmissionBandwidth returns how many new reliable bytes may be sent now,
// that is the room left in the window.
func (c *congestionWindow) transmissionBandwidth() uint32 {
	if float64(c.inFlight) >= c.window {
		return 0
	}
	// Clamped because converting a float64 above math.MaxUint32 is undefined.
	return uint32(min(c.window-float64(c.inFlight), math.MaxUint32))
}

// slowStart reports whether the window is still growing by a full MTU per ACK.
func (c *congestionWindow) slowStart() bool {
	return c.window <= c.threshold || c.threshold == 0
}

// sent and acknowledged maintain the invariant that inFlight equals the sum of
// the inFlightBytes of every reliable record still waiting for an ACK.
func (c *congestionWindow) sent(bytes uint32) {
	c.inFlight += bytes
}

func (c *congestionWindow) acknowledged(bytes uint32) {
	if bytes >= c.inFlight {
		c.inFlight = 0
		return
	}
	c.inFlight -= bytes
}

// ack grows the window for one acknowledged datagram: by a full MTU per ACK
// during slow start, then by the Reno additive increase once past the
// threshold. nextSequence is the next sequence number the sender will use, used
// to scope recovery to one congestion block.
func (c *congestionWindow) ack(sequence, nextSequence uint24) {
	if !c.continuous {
		return
	}

	newBlock := sequenceGreaterThan(sequence, c.nextBlock)
	if newBlock {
		c.backedOff = false
		c.nextBlock = nextSequence
	}
	if c.slowStart() {
		c.window += float64(c.mtu)
		if c.threshold == 0 || c.window <= c.threshold {
			return
		}
		c.window = c.threshold + float64(c.mtu*c.mtu)/c.window
		return
	}
	// Congestion blocks scope recovery state; continuous ACKs use Reno additive increase.
	c.window += float64(c.mtu*c.mtu) / c.window
}

// nak reacts to a NAK: halve the threshold so the window stops growing, but
// leave the window itself alone. At most once per congestion block.
func (c *congestionWindow) nak(nextSequence uint24) {
	if c.continuous && !c.backedOff {
		c.threshold = c.window / 2
		c.nextBlock = nextSequence
		c.backedOff = true
	}
}

// resend reacts to a retransmission timeout, the harder signal: halve the
// threshold and drop the window back to one MTU. At most once per block, and
// only once the window is large enough to be worth cutting.
func (c *congestionWindow) resend(nextSequence uint24) {
	if !c.continuous || c.backedOff || c.window <= float64(c.mtu*2) {
		return
	}
	c.threshold = max(c.window/2, float64(c.mtu))
	c.window = float64(c.mtu)
	c.nextBlock = nextSequence
	c.backedOff = true
}

// sequenceGreaterThan reports whether a is ahead of b in 24-bit sequence space,
// treating the nearer half of the ring as "ahead" so wraparound compares right.
func sequenceGreaterThan(a, b uint24) bool {
	const half = uint24(0x7fffff)
	return a != b && (b-a)&0xffffff > half
}
