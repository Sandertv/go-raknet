package raknet

import (
	"encoding/binary"
	"time"

	"github.com/sandertv/go-raknet/internal/message"
)

// sendMTUProbeSizes are tried in order after InitialSendMTU, up to the
// handshake-negotiated MTU. 1280 is IPv6/WARP, 1400 is vanilla BDS, 1492 is max.
var sendMTUProbeSizes = []uint16{1280, 1400, maxMTUSize}

const (
	// reliableProbeOverhead is datagram flags+seq + packet header+len + message index.
	reliableProbeOverhead = 1 + 3 + 1 + 2 + 3
	mtuProbeMaxAttempts   = 3
)

// sendMTUDiscovery tracks post-handshake upward probes of the send path.
// The negotiated conn.mtu stays whatever the client agreed; we only raise
// sendMTU after a probe datagram of that size is ACKed. Vanilla clients do
// not raise their own send MTU — this is server/dialer send-path only.
type sendMTUDiscovery struct {
	done     bool
	pending  uint24
	size     uint16
	sentAt   time.Time
	attempts int
}

func (conn *Conn) maybeProbeSendMTU(now time.Time) {
	if conn.discover.done || uint16(conn.sendMTU.Load()) >= conn.mtu {
		conn.discover.done = true
		return
	}
	select {
	case <-conn.connected:
	default:
		return
	}

	if conn.discover.pending != 0 {
		timeout := max(500*time.Millisecond, time.Duration(conn.rtt.Load())*3)
		if now.Sub(conn.discover.sentAt) < timeout {
			return
		}
		// Drop the oversized datagram so checkResend does not keep blasting
		// it at a path that cannot take it.
		if pk, ok := conn.retransmission.acknowledge(conn.discover.pending); ok {
			pk.content = pk.content[:0]
			packetPool.Put(pk)
		}
		conn.discover.pending = 0
		conn.discover.attempts++
		if conn.discover.attempts >= mtuProbeMaxAttempts {
			conn.discover.done = true
			return
		}
		conn.sendSendMTUProbe(conn.discover.size, now)
		return
	}

	next := conn.nextSendMTUProbe()
	if next == 0 {
		conn.discover.done = true
		return
	}
	conn.discover.attempts = 0
	conn.sendSendMTUProbe(next, now)
}

func (conn *Conn) nextSendMTUProbe() uint16 {
	cur := uint16(conn.sendMTU.Load())
	for _, s := range sendMTUProbeSizes {
		if s > cur && s <= conn.mtu {
			return s
		}
	}
	return 0
}

func (conn *Conn) sendSendMTUProbe(target uint16, now time.Time) {
	udp := int(target - 28)
	contentLen := udp - reliableProbeOverhead
	if contentLen < 9 {
		conn.discover.done = true
		return
	}
	content := make([]byte, contentLen)
	content[0] = message.IDConnectedPing
	binary.BigEndian.PutUint64(content[1:], uint64(timestamp()))

	pk := packetPool.Get().(*packet)
	pk.reliability = reliabilityReliable
	pk.split = false
	pk.content = content
	pk.messageIndex = conn.messageIndex.Inc()

	seq, err := conn.sendDatagram(pk)
	if err != nil {
		pk.content = pk.content[:0]
		packetPool.Put(pk)
		return
	}
	conn.discover.pending = seq
	conn.discover.size = target
	conn.discover.sentAt = now
}

func (conn *Conn) onProbeAck(seq uint24) {
	if conn.discover.pending == 0 || seq != conn.discover.pending {
		return
	}
	if conn.discover.size > uint16(conn.sendMTU.Load()) && conn.discover.size <= conn.mtu {
		conn.sendMTU.Store(uint32(conn.discover.size))
		conn.notifySendMTU()
	}
	conn.discover.pending = 0
	conn.discover.attempts = 0
	conn.discover.size = 0
}

func (d *sendMTUDiscovery) isPending(seq uint24) bool {
	return d.pending != 0 && d.pending == seq
}

func (conn *Conn) notifySendMTU() {
	if conn.onSendMTU == nil {
		return
	}
	conn.onSendMTU(conn.raddr, uint16(conn.sendMTU.Load()))
}
