package raknet

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	// bitFlagDatagram is set for every valid datagram. It is used to identify packets that are datagrams.
	bitFlagDatagram = 0x80
	// bitFlagACK is set for every ACK packet.
	bitFlagACK = 0x40
	// bitFlagNACK is set for every NACK packet.
	bitFlagNACK = 0x20
	// bitFlagNeedsBAndAS is set for every datagram with packet data, but is not
	// actually used.
	bitFlagNeedsBAndAS = 0x04
)

//noinspection GoUnusedConst
const (
	// reliabilityUnreliable means that the packet sent could arrive out of order, be duplicated, or just not
	// arrive at all. It is usually used for high frequency packets of which the order does not matter.
	//lint:ignore U1000 While this constant is unused, it is here for the sake of having all reliabilities.
	reliabilityUnreliable byte = iota
	// reliabilityUnreliableSequenced means that the packet sent could be duplicated or not arrive at all, but
	// ensures that it is always handled in the right order.
	reliabilityUnreliableSequenced
	// reliabilityReliable means that the packet sent could not arrive, or arrive out of order, but ensures
	// that the packet is not duplicated.
	reliabilityReliable
	// reliabilityReliableOrdered means that every packet sent arrives, arrives in the right order and is not
	// duplicated.
	reliabilityReliableOrdered
	// reliabilityReliableSequenced means that the packet sent could not arrive, but ensures that the packet
	// will be in the right order and not be duplicated.
	reliabilityReliableSequenced

	// splitFlag is set in the header if the packet was split. If so, the encapsulation contains additional
	// data about the fragment.
	splitFlag = 0x10
)

// packet is an encapsulation around every packet sent after the connection is established. It is
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
func (packet *packet) write(b *bytes.Buffer) {
	header := packet.reliability << 5
	if packet.split {
		header |= splitFlag
	}
	b.WriteByte(header)
	_ = binary.Write(b, binary.BigEndian, uint16(len(packet.content))<<3)
	if packet.reliable() {
		writeUint24(b, packet.messageIndex)
	}
	if packet.sequenced() {
		writeUint24(b, packet.sequenceIndex)
	}
	if packet.sequencedOrOrdered() {
		writeUint24(b, packet.orderIndex)
		// Order channel, we don't care about this.
		b.WriteByte(0)
	}
	if packet.split {
		_ = binary.Write(b, binary.BigEndian, packet.splitCount)
		_ = binary.Write(b, binary.BigEndian, packet.splitID)
		_ = binary.Write(b, binary.BigEndian, packet.splitIndex)
	}
	b.Write(packet.content)
}

// read reads a packet and its content from the buffer passed.
func (packet *packet) read(b *bytes.Buffer) error {
	header, err := b.ReadByte()
	if err != nil {
		return fmt.Errorf("error reading packet header: %v", err)
	}
	packet.split = (header & splitFlag) != 0
	packet.reliability = (header & 224) >> 5
	var packetLength uint16
	if err := binary.Read(b, binary.BigEndian, &packetLength); err != nil {
		return fmt.Errorf("error reading packet length: %v", err)
	}
	packetLength >>= 3
	if packetLength == 0 {
		return fmt.Errorf("invalid packet length: cannot be 0")
	}

	if packet.reliable() {
		packet.messageIndex, err = readUint24(b)
		if err != nil {
			return fmt.Errorf("error reading packet message index: %v", err)
		}
	}

	if packet.sequenced() {
		packet.sequenceIndex, err = readUint24(b)
		if err != nil {
			return fmt.Errorf("error reading packet sequence index: %v", err)
		}
	}

	if packet.sequencedOrOrdered() {
		packet.orderIndex, err = readUint24(b)
		if err != nil {
			return fmt.Errorf("error reading packet order index: %v", err)
		}
		// Order channel (byte), we don't care about this.
		b.Next(1)
	}

	if packet.split {
		if err := binary.Read(b, binary.BigEndian, &packet.splitCount); err != nil {
			return fmt.Errorf("error reading packet split count: %v", err)
		}
		if err := binary.Read(b, binary.BigEndian, &packet.splitID); err != nil {
			return fmt.Errorf("error reading packet split ID: %v", err)
		}
		if err := binary.Read(b, binary.BigEndian, &packet.splitIndex); err != nil {
			return fmt.Errorf("error reading packet split index: %v", err)
		}
	}

	packet.content = make([]byte, packetLength)
	if n, err := b.Read(packet.content); err != nil || n != int(packetLength) {
		return fmt.Errorf("not enough data in packet: %v bytes read but need %v", n, packetLength)
	}
	return nil
}

func (packet *packet) reliable() bool {
	switch packet.reliability {
	case reliabilityReliable,
		reliabilityReliableOrdered,
		reliabilityReliableSequenced:
		return true
	}
	return false
}

func (packet *packet) sequencedOrOrdered() bool {
	switch packet.reliability {
	case reliabilityUnreliableSequenced,
		reliabilityReliableOrdered,
		reliabilityReliableSequenced:
		return true
	}
	return false
}

func (packet *packet) sequenced() bool {
	switch packet.reliability {
	case reliabilityUnreliableSequenced,
		reliabilityReliableSequenced:
		return true
	}
	return false
}

const (
	// packetRange indicates a range of packets, followed by the first and the last packet in the range.
	packetRange = iota
	// packetSingle indicates a single packet, followed by its sequence number.
	packetSingle
)

// acknowledgement is an acknowledgement packet that may either be an ACK or a NACK, depending on the purpose
// that it is sent with.
type acknowledgement struct {
	packets []uint24
}

// write encodes an acknowledgement packet and returns an error if not successful.
func (ack *acknowledgement) write(b *bytes.Buffer, mtu uint16) (n int, err error) {
	packets := ack.packets
	if len(packets) == 0 {
		return 0, binary.Write(b, binary.BigEndian, int16(0))
	}
	buffer := bytes.NewBuffer(nil)
	// Sort packets before encoding to ensure packets are encoded correctly.
	sort.Slice(packets, func(i, j int) bool {
		return packets[i] < packets[j]
	})

	var firstPacketInRange uint24
	var lastPacketInRange uint24
	var recordCount int16

	for index, packet := range packets {
		if buffer.Len() >= int(mtu-10) {
			// We must make sure the final packet length doesn't exceed the MTU size.
			break
		}
		n++
		if index == 0 {
			// The first packet, set the first and last packet to it.
			firstPacketInRange = packet
			lastPacketInRange = packet
			continue
		}
		if packet == lastPacketInRange+1 {
			// Packet is still part of the current range, as it's sequenced properly with the last packet.
			// Set the last packet in range to the packet and continue to the next packet.
			lastPacketInRange = packet
			continue
		} else {
			// We got to the end of a range/single packet. We need to write those down now.
			if firstPacketInRange == lastPacketInRange {
				// First packet equals last packet, so we have a single packet record. Write down the packet,
				// and set the first and last packet to the current packet.
				if err := buffer.WriteByte(packetSingle); err != nil {
					return 0, err
				}
				writeUint24(buffer, firstPacketInRange)

				firstPacketInRange = packet
				lastPacketInRange = packet
			} else {
				// There's a gap between the first and last packet, so we have a range of packets. Write the
				// first and last packet of the range and set both to the current packet.
				if err := buffer.WriteByte(packetRange); err != nil {
					return 0, err
				}
				writeUint24(buffer, firstPacketInRange)
				writeUint24(buffer, lastPacketInRange)

				firstPacketInRange = packet
				lastPacketInRange = packet
			}
			// Keep track of the amount of records as we need to write that first.
			recordCount++
		}
	}

	// Make sure the last single packet/range is written, as we always need to know one packet ahead to know
	// how we should write the current.
	if firstPacketInRange == lastPacketInRange {
		if err := buffer.WriteByte(packetSingle); err != nil {
			return 0, err
		}
		writeUint24(buffer, firstPacketInRange)
	} else {
		if err := buffer.WriteByte(packetRange); err != nil {
			return 0, err
		}
		writeUint24(buffer, firstPacketInRange)
		writeUint24(buffer, lastPacketInRange)
	}
	recordCount++
	if err := binary.Write(b, binary.BigEndian, recordCount); err != nil {
		return 0, err
	}
	if _, err := b.Write(buffer.Bytes()); err != nil {
		return 0, err
	}
	return n, nil
}

// read decodes an acknowledgement packet and returns an error if not successful.
func (ack *acknowledgement) read(b *bytes.Buffer) error {
	const maxAcknowledgementPackets = 8192
	var recordCount int16
	if err := binary.Read(b, binary.BigEndian, &recordCount); err != nil {
		return err
	}
	for i := int16(0); i < recordCount; i++ {
		recordType, err := b.ReadByte()
		if err != nil {
			return err
		}
		switch recordType {
		case packetRange:
			start, err := readUint24(b)
			if err != nil {
				return err
			}
			end, err := readUint24(b)
			if err != nil {
				return err
			}
			for pack := start; pack <= end; pack++ {
				ack.packets = append(ack.packets, pack)
				if len(ack.packets) > maxAcknowledgementPackets {
					return fmt.Errorf("maximum amount of packets in acknowledgement exceeded")
				}
			}
		case packetSingle:
			packet, err := readUint24(b)
			if err != nil {
				return err
			}
			ack.packets = append(ack.packets, packet)
			if len(ack.packets) > maxAcknowledgementPackets {
				return fmt.Errorf("maximum amount of packets in acknowledgement exceeded")
			}
		}
	}
	return nil
}
