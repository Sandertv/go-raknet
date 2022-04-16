package raknet

import (
	"time"
)

// resendMap is a map of packets, used to recover datagrams if the other end of the connection ended up
// not having them.
type resendMap struct {
	unacknowledged map[uint24]resendRecord
	delays         map[time.Time]time.Duration
}

// resendRecord represents a single packet with a timestamp from when it was initially sent. It may be either
// acknowledged or NACKed by the other end.
type resendRecord struct {
	pk        *packet
	timestamp time.Time
}

// newRecoveryQueue returns a new initialised recovery queue.
func newRecoveryQueue() *resendMap {
	return &resendMap{
		delays:         make(map[time.Time]time.Duration),
		unacknowledged: make(map[uint24]resendRecord),
	}
}

// add puts a packet at the index passed and records the current time.
func (m *resendMap) add(index uint24, pk *packet) {
	m.unacknowledged[index] = resendRecord{pk: pk, timestamp: time.Now()}
}

// acknowledge marks a packet with the index passed as acknowledged. The packet is removed from the resendMap and
// returned if found.
func (m *resendMap) acknowledge(index uint24) (*packet, bool) {
	return m.remove(index, 1)
}

// retransmit looks up a packet with an index from the resendMap so that it may be resent.
func (m *resendMap) retransmit(index uint24) (*packet, bool) {
	return m.remove(index, 2)
}

// remove deletes an index from the resendMap and adds the time since the packet was originally sent multiplied by mul
// to the delays slice.
func (m *resendMap) remove(index uint24, mul int) (*packet, bool) {
	record, ok := m.unacknowledged[index]
	if !ok {
		return nil, false
	}
	delete(m.unacknowledged, index)

	now := time.Now()
	m.delays[now] = now.Sub(record.timestamp) * time.Duration(mul)
	return record.pk, true
}

// rtt returns the average round trip time between the putting of the value into the recovery queue and the taking
// out of it again. It is measured over the last delayRecordCount values add in.
func (m *resendMap) rtt() time.Duration {
	const averageDuration = time.Second * 5
	var (
		total, records time.Duration
		now            = time.Now()
	)
	for t, rtt := range m.delays {
		if now.Sub(t) > averageDuration {
			delete(m.delays, t)
			continue
		}
		total += rtt
		records++
	}
	if records == 0 {
		// No records yet, generally should not happen. Just return a reasonable amount of time.
		return time.Millisecond * 50
	}
	return total / records
}
