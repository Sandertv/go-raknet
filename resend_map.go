package raknet

import (
	"time"
)

// resendMap is a map of packets, used to recover datagrams if the other end of
// the connection ended up not having them.
type resendMap struct {
	unacknowledged map[uint24]resendRecord
	estimatedRTT   time.Duration
	deviationRTT   time.Duration
	hasRTT         bool
	// deadline is a lower bound on the earliest nextSend of any record. It lets
	// a frequently woken send loop skip the scan when nothing is due yet.
	deadline time.Time
}

// resendRecord represents a single packet with a timestamp from when it was
// initially sent. It may be either acknowledged or NACKed by the other end.
type resendRecord struct {
	pk            *packet
	inFlightBytes uint32
	timestamp     time.Time
	nextSend      time.Time
}

// newRecoveryQueue returns a new initialised recovery queue.
func newRecoveryQueue() *resendMap {
	return &resendMap{
		unacknowledged: make(map[uint24]resendRecord),
	}
}

// add puts a packet at the index passed and records the current time.
func (m *resendMap) add(index uint24, pk *packet, inFlightBytes uint32) {
	now := time.Now()
	nextSend := now.Add(m.rto())
	m.unacknowledged[index] = resendRecord{
		pk: pk, inFlightBytes: inFlightBytes, timestamp: now, nextSend: nextSend,
	}
	m.lowerDeadline(nextSend)
}

// lowerDeadline keeps deadline a lower bound on every record's nextSend.
func (m *resendMap) lowerDeadline(t time.Time) {
	if m.deadline.IsZero() || t.Before(m.deadline) {
		m.deadline = t
	}
}

// due reports whether any record may have become eligible for retransmission.
func (m *resendMap) due(now time.Time) bool {
	return !m.deadline.IsZero() && !now.Before(m.deadline)
}

// acknowledge marks a packet with the index passed as acknowledged. The packet
// is removed from the resendMap and returned if found.
func (m *resendMap) acknowledge(index uint24) (resendRecord, bool) {
	record, ok := m.remove(index)
	if ok {
		m.observeRTT(time.Since(record.timestamp))
	}
	return record, ok
}

// retransmit looks up a packet with an index from the resendMap so that it may
// be resent.
func (m *resendMap) retransmit(index uint24) (resendRecord, bool) {
	return m.remove(index)
}

func (m *resendMap) remove(index uint24) (resendRecord, bool) {
	record, ok := m.unacknowledged[index]
	if !ok {
		return resendRecord{}, false
	}
	delete(m.unacknowledged, index)
	return record, true
}

// observeRTT smooths RTT and deviation so one unusual sample has little effect.
func (m *resendMap) observeRTT(sample time.Duration) {
	if !m.hasRTT {
		m.estimatedRTT = sample
		m.deviationRTT = sample
		m.hasRTT = true
		return
	}
	difference := sample - m.estimatedRTT
	m.estimatedRTT += time.Duration(float64(difference) * 0.05)
	if difference < 0 {
		difference = -difference
	}
	m.deviationRTT += time.Duration(float64(difference-m.deviationRTT) * 0.05)
}

func (m *resendMap) rtt() time.Duration {
	if !m.hasRTT {
		return time.Millisecond * 50
	}
	return m.estimatedRTT
}

// rto uses the smoothed RTT and deviation. A record becomes eligible for
// retransmission when its send time plus this duration has passed.
func (m *resendMap) rto() time.Duration {
	if !m.hasRTT {
		return time.Second * 2
	}
	return min(m.estimatedRTT*2+m.deviationRTT*4+time.Millisecond*30, time.Second*2)
}
