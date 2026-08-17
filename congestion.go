package raknet

import "math"

// congestionWindow limits reliable bytes in flight. Counts use encapsulated
// packet sizes and exclude the 4-byte datagram header. ACKs grow the window
// through slow start and then congestion avoidance. A NAK begins recovery by
// lowering the threshold; a timeout also resets the window to one MTU.
type congestionWindow struct {
	mtu        uint32
	window     float64
	threshold  float64
	inFlight   uint32
	nextBlock  uint24 // sequence number that starts the next congestion block
	backedOff  bool   // window already cut for the current block
	continuous bool   // data still queued last tick; the window only grows then
}

func newCongestionWindow(mtu uint16) congestionWindow {
	return congestionWindow{mtu: uint32(mtu), window: float64(mtu)}
}

// transmissionBandwidth returns the unused room in the window, in bytes.
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

// ack grows the window for an acknowledged datagram: a full MTU per ACK during
// slow start, then Reno additive increase.
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

// nak halves the threshold on a NAK so the window stops growing, without cutting
// it. At most once per congestion block.
func (c *congestionWindow) nak(nextSequence uint24) {
	if c.continuous && !c.backedOff {
		c.threshold = c.window / 2
		c.nextBlock = nextSequence
		c.backedOff = true
	}
}

// resend handles a retransmission timeout: halve the threshold and cut the
// window to one MTU. At most once per block.
func (c *congestionWindow) resend(nextSequence uint24) {
	if !c.continuous || c.backedOff || c.window <= float64(c.mtu*2) {
		return
	}
	c.threshold = max(c.window/2, float64(c.mtu))
	c.window = float64(c.mtu)
	c.nextBlock = nextSequence
	c.backedOff = true
}

// sequenceGreaterThan reports whether a is ahead of b in wrapping 24-bit space.
func sequenceGreaterThan(a, b uint24) bool {
	const half = uint24(0x7fffff)
	return a != b && (b-a)&0xffffff > half
}
