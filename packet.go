package raknet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"slices"
)

const (
	// bitFlagDatagram is set for every valid datagram. It is used to identify
	// packets that are datagrams.
	bitFlagDatagram = 0x80
	// bitFlagACK is set for every ACK packet.
	bitFlagACK = 0x40
	// bitFlagNACK is set for every NACK packet.
	bitFlagNACK = 0x20
	// bitFlagNeedsBAndAS is set for every datagram with packet data, but is not
	// actually used.
	bitFlagNeedsBAndAS = 0x04
)

// noinspection GoUnusedConst
const (
	// reliabilityUnreliable means that the packet sent could arrive out of
	// order, be duplicated, or just not arrive at all. It is usually used for
	// high frequency packets of which the order does not matter.
	//lint:ignore U1000 While this constant is unused, it is here for the sake
	// of having all reliabilities documented.
	reliabilityUnreliable byte = iota
	// reliabilityUnreliableSequenced means that the packet sent could be
	// duplicated or not arrive at all, but ensures that it is always handled in
	// the right order.
	reliabilityUnreliableSequenced
	// reliabilityReliable means that the packet sent could not arrive, or
	// arrive out of order, but ensures that the packet is not duplicated.
	reliabilityReliable
	// reliabilityReliableOrdered means that every packet sent arrives, arrives
	// in the right order and is not duplicated.
	reliabilityReliableOrdered
	// reliabilityReliableSequenced means that the packet sent could not arrive,
	// but ensures that the packet will be in the right order and not be
	// duplicated.
	reliabilityReliableSequenced

	// splitFlag is set in the header if the packet was split. If so, the
	// encapsulation contains additional data about the fragment.
	splitFlag = 0x10
)

// packet is an encapsulation around every packet sent after the connection is
// established.
type packet struct {
	reliability byte

	content       []byte
	messageIndex  uint24
	sequenceIndex uint24
	orderIndex    uint24

	split      bool
	splitCount uint32
	splitIndex uint32
	splitID    uint16
}

// write writes the packet and its content to the buffer passed.
func (pk *packet) write(buf *bytes.Buffer) {
	header := pk.reliability << 5
	if pk.split {
		header |= splitFlag
	}

	buf.WriteByte(header)
	writeUint16(buf, uint16(len(pk.content))<<3)
	if pk.reliable() {
		writeUint24(buf, pk.messageIndex)
	}
	if pk.sequenced() {
		writeUint24(buf, pk.sequenceIndex)
	}
	if pk.sequencedOrOrdered() {
		writeUint24(buf, pk.orderIndex)
		// Order channel, we don't care about this.
		buf.WriteByte(0)
	}
	if pk.split {
		writeUint32(buf, pk.splitCount)
		writeUint16(buf, pk.splitID)
		writeUint32(buf, pk.splitIndex)
	}
	buf.Write(pk.content)
}

// read reads a packet and its content from the buffer passed.
func (pk *packet) read(buf *bytes.Buffer) error {
	header, err := buf.ReadByte()
	if err != nil {
		return io.ErrUnexpectedEOF
	}
	pk.split = (header & splitFlag) != 0
	pk.reliability = (header & 224) >> 5
	packetLength, err := readUint16(buf)
	if err != nil {
		return io.ErrUnexpectedEOF
	}
	packetLength >>= 3
	if packetLength == 0 {
		return errors.New("invalid packet length: cannot be 0")
	}

	if pk.reliable() {
		if pk.messageIndex, err = readUint24(buf); err != nil {
			return io.ErrUnexpectedEOF
		}
	}

	if pk.sequenced() {
		if pk.sequenceIndex, err = readUint24(buf); err != nil {
			return io.ErrUnexpectedEOF
		}
	}

	if pk.sequencedOrOrdered() {
		if pk.orderIndex, err = readUint24(buf); err != nil {
			return io.ErrUnexpectedEOF
		}
		// Order channel (byte), we don't care about this.
		buf.Next(1)
	}

	if pk.split {
		pk.splitCount, _ = readUint32(buf)
		pk.splitID, _ = readUint16(buf)
		if pk.splitIndex, err = readUint32(buf); err != nil {
			return io.ErrUnexpectedEOF
		}
	}

	pk.content = make([]byte, packetLength)
	if n, err := buf.Read(pk.content); err != nil || n != int(packetLength) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (pk *packet) reliable() bool {
	switch pk.reliability {
	case reliabilityReliable,
		reliabilityReliableOrdered,
		reliabilityReliableSequenced:
		return true
	}
	return false
}

func (pk *packet) sequencedOrOrdered() bool {
	switch pk.reliability {
	case reliabilityUnreliableSequenced,
		reliabilityReliableOrdered,
		reliabilityReliableSequenced:
		return true
	}
	return false
}

func (pk *packet) sequenced() bool {
	switch pk.reliability {
	case reliabilityUnreliableSequenced,
		reliabilityReliableSequenced:
		return true
	}
	return false
}

const (
	// packetRange indicates a range of packets, followed by the first and the
	// last packet in the range.
	packetRange = iota
	// packetSingle indicates a single packet, followed by its sequence number.
	packetSingle
)

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
		if buf.Len() >= int(mtu-(28+10)) {
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
func (ack *acknowledgement) read(buf *bytes.Buffer) error {
	const maxAcknowledgementPackets = 8192
	recordCount, err := readUint16(buf)
	if err != nil {
		return io.ErrUnexpectedEOF
	}
	for i := uint16(0); i < recordCount; i++ {
		recordType, err := buf.ReadByte()
		if err != nil {
			return io.ErrUnexpectedEOF
		}
		switch recordType {
		case packetRange:
			start, _ := readUint24(buf)
			end, err := readUint24(buf)
			if err != nil {
				return io.ErrUnexpectedEOF
			}
			if uint24(len(ack.packets))+end-start > maxAcknowledgementPackets {
				return errMaxAcknowledgement
			}
			for pk := start; pk <= end; pk++ {
				ack.packets = append(ack.packets, pk)
			}
		case packetSingle:
			if len(ack.packets)+1 > maxAcknowledgementPackets {
				return errMaxAcknowledgement
			}
			pk, err := readUint24(buf)
			if err != nil {
				return io.ErrUnexpectedEOF
			}
			ack.packets = append(ack.packets, pk)
		}
	}
	return nil
}

var errMaxAcknowledgement = errors.New("maximum amount of packets in acknowledgement exceeded")
