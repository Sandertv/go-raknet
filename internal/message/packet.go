package message

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
)

const (
	IDConnectedPing                  byte = 0x00
	IDUnconnectedPing                byte = 0x01
	IDUnconnectedPingOpenConnections byte = 0x02
	IDConnectedPong                  byte = 0x03
	IDDetectLostConnections          byte = 0x04
	IDOpenConnectionRequest1         byte = 0x05
	IDOpenConnectionReply1           byte = 0x06
	IDOpenConnectionRequest2         byte = 0x07
	IDOpenConnectionReply2           byte = 0x08
	IDConnectionRequest              byte = 0x09
	IDConnectionRequestAccepted      byte = 0x10
	IDNewIncomingConnection          byte = 0x13
	IDDisconnectNotification         byte = 0x15

	IDIncompatibleProtocolVersion byte = 0x19

	IDUnconnectedPong byte = 0x1c
)

// unconnectedMessageSequence is a sequence of bytes which is found in every unconnected message sent in
// RakNet.
var unconnectedMessageSequence = [16]byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

// writeAddr writes a UDP address to the buffer passed.
func writeAddr(buffer *bytes.Buffer, addr net.UDPAddr) {
	var ver byte = 6
	if addr.IP.To4() != nil {
		ver = 4
	}
	if addr.IP == nil {
		addr.IP = make([]byte, 16)
	}
	_ = buffer.WriteByte(ver)
	if ver == 4 {
		ipBytes := addr.IP.To4()

		_ = buffer.WriteByte(^ipBytes[0])
		_ = buffer.WriteByte(^ipBytes[1])
		_ = buffer.WriteByte(^ipBytes[2])
		_ = buffer.WriteByte(^ipBytes[3])
		_ = binary.Write(buffer, binary.BigEndian, uint16(addr.Port))
	} else {
		_ = binary.Write(buffer, binary.LittleEndian, int16(23)) // syscall.AF_INET6 on Windows.
		_ = binary.Write(buffer, binary.BigEndian, uint16(addr.Port))
		// The IPv6 address is enclosed in two 0 integers.
		_ = binary.Write(buffer, binary.BigEndian, int32(0))
		_, _ = buffer.Write(addr.IP.To16())
		_ = binary.Write(buffer, binary.BigEndian, int32(0))
	}
}

// readAddr decodes a RakNet address from the buffer passed. If not successful, an error is returned.
func readAddr(buffer *bytes.Buffer, addr *net.UDPAddr) error {
	ver, err := buffer.ReadByte()
	if err != nil {
		return err
	}
	if ver == 4 {
		ipBytes := make([]byte, 4)
		if _, err := buffer.Read(ipBytes); err != nil {
			return fmt.Errorf("error reading raknet address ipv4 bytes: %v", err)
		}
		// Construct an IPv4 out of the 4 bytes we just read.
		addr.IP = net.IPv4((-ipBytes[0]-1)&0xff, (-ipBytes[1]-1)&0xff, (-ipBytes[2]-1)&0xff, (-ipBytes[3]-1)&0xff)
		var port uint16
		if err := binary.Read(buffer, binary.BigEndian, &port); err != nil {
			return fmt.Errorf("error reading raknet address port: %v", err)
		}
		addr.Port = int(port)
	} else {
		buffer.Next(2)
		var port uint16
		if err := binary.Read(buffer, binary.LittleEndian, &port); err != nil {
			return fmt.Errorf("error reading raknet address port: %v", err)
		}
		addr.Port = int(port)
		buffer.Next(4)
		addr.IP = make([]byte, 16)
		if _, err := buffer.Read(addr.IP); err != nil {
			return fmt.Errorf("error reading raknet address ipv6 bytes: %v", err)
		}
		buffer.Next(4)
	}
	return nil
}
