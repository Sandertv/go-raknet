package raknet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"slices"
)

const (
	// packetRange indicates a range of packets, followed by the first and the
	// last packet in the range.
	packetRange = iota
	// packetSingle indicates a single packet, followed by its sequence number.
	packetSingle
)

var errMaxAcknowledgement = errors.New("maximum amount of packets in acknowledgement exceeded")

// acknowledgement is an acknowledgement packet that may either be an ACK or a
// NACK, depending on the purpose that it is sent with.
type acknowledgement struct {
	packets []uint24
}

// write encodes an acknowledgement packet and returns an error if not
// successful.
func (ack *acknowledgement) write(buf *bytes.Buffer, mtu uint16) int {
	lenOffset := buf.Len()
	writeUint16(buf, 0) // Placeholder for record count.

	packets := ack.packets
	if len(packets) == 0 {
		return 0
	}

	var firstPacketInRange, lastPacketInRange uint24
	var records uint16
	n := 0

	// Sort packets before encoding to ensure packets are encoded correctly.
	slices.Sort(packets)

	for index, pk := range packets {
		if buf.Len() >= int(mtu-7) {
			// We must make sure the final packet length doesn't exceed the MTU
			// size.
			break
		}
		n++
		if index == 0 {
			// The first packet, set the first and last packet to it.
			firstPacketInRange, lastPacketInRange = pk, pk
			continue
		}
		if pk == lastPacketInRange+1 {
			// Packet is still part of the current range, as it's sequenced
			// properly with the last packet. Set the last packet in range to
			// the packet and continue to the next packet.
			lastPacketInRange = pk
			continue
		}
		ack.writeRecord(buf, firstPacketInRange, lastPacketInRange, &records)
		firstPacketInRange, lastPacketInRange = pk, pk
	}
	// Make sure the last single packet/range is written, as we always need to
	// know one packet ahead to know how we should write the current.
	ack.writeRecord(buf, firstPacketInRange, lastPacketInRange, &records)

	binary.BigEndian.PutUint16(buf.Bytes()[lenOffset:], records)
	return n
}

func (ack *acknowledgement) writeRecord(buf *bytes.Buffer, first, last uint24, count *uint16) {
	if first == last {
		// First packet equals last packet, so we have a single packet
		// record. Write down the packet, and set the first and last
		// packet to the current packet.
		buf.WriteByte(packetSingle)
		writeUint24(buf, first)
	} else {
		// There's a gap between the first and last packet, so we have a
		// range of packets. Write the first and last packet of the
		// range and set both to the current packet.
		buf.WriteByte(packetRange)
		writeUint24(buf, first)
		writeUint24(buf, last)
	}
	*count++
}

// read decodes an acknowledgement packet and returns an error if not
// successful.
func (ack *acknowledgement) read(b []byte) error {
	const maxAcknowledgementPackets = 8192
	if len(b) < 2 {
		return io.ErrUnexpectedEOF
	}
	offset := 2
	for range binary.BigEndian.Uint16(b) {
		if len(b)-offset < 4 {
			return io.ErrUnexpectedEOF
		}
		switch b[offset] {
		case packetRange:
			if len(b)-offset < 7 {
				return io.ErrUnexpectedEOF
			}
			start, end := loadUint24(b[offset+1:]), loadUint24(b[offset+4:])
			if uint24(len(ack.packets))+end-start > maxAcknowledgementPackets {
				return errMaxAcknowledgement
			}
			for pk := start; pk <= end; pk++ {
				ack.packets = append(ack.packets, pk)
			}
			offset += 7
		case packetSingle:
			if len(ack.packets)+1 > maxAcknowledgementPackets {
				return errMaxAcknowledgement
			}
			ack.packets = append(ack.packets, loadUint24(b[offset+1:]))
			offset += 4
		}
	}
	return nil
}
