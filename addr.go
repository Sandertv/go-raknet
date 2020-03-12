package raknet

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
)

// rakAddr is a wrapper around a net.UDPAddr that provides Marshal- and UnmarshalBinary methods for RakNet
// specific address writing.
type rakAddr net.UDPAddr

// UnmarshalBinary implements the binary decoding method for RakNet specific address encoding.
func (addr *rakAddr) UnmarshalBinary(b []byte) error {
	buffer := bytes.NewBuffer(b)
	if addr == nil {
		// No address was set. We create an empty address in which we will decode the values we find.
		v := rakAddr(net.UDPAddr{})
		//noinspection GoAssignmentToReceiver
		addr = &v
	}
	ver, err := buffer.ReadByte()
	if err != nil {
		return fmt.Errorf("error reading raknet address version byte: %v", err)
	}
	if ver == 4 {
		ipBytes := make([]byte, 4)
		if _, err := buffer.Read(ipBytes); err != nil {
			return fmt.Errorf("error reading raknet address ipv4 bytes: %v", err)
		}
		// Construct an IPv4 out of the 4 bytes we just read.
		addr.IP = net.IPv4(
			(-ipBytes[0]-1)&0xff,
			(-ipBytes[1]-1)&0xff,
			(-ipBytes[2]-1)&0xff,
			(-ipBytes[3]-1)&0xff,
		)
		var port int16
		if err := binary.Read(buffer, binary.BigEndian, &port); err != nil {
			return fmt.Errorf("error reading raknet address port: %v", err)
		}
		addr.Port = int(port)
	} else {
		// Pass the first short, we don't care about it.
		buffer.Next(2)
		var port int16
		if err := binary.Read(buffer, binary.BigEndian, &port); err != nil {
			return fmt.Errorf("error reading raknet address port: %v", err)
		}
		addr.Port = int(port)

		// Pass the integer at this offset.
		buffer.Next(4)

		addr.IP = make([]byte, 16)
		if _, err := buffer.Read(addr.IP); err != nil {
			return fmt.Errorf("error reading raknet address ipv6 bytes: %v", err)
		}

		// Pass another integer at this offset.
		buffer.Next(4)
	}
	return nil
}

// MarshalBinary implements the binary encoding method for RakNet specific addresses.
func (addr *rakAddr) MarshalBinary() (b []byte, err error) {
	buffer := bytes.NewBuffer(b)
	if addr == nil {
		// No address was set. As a replacement, we write a zero value address, so that RakNet is happy.
		v := rakAddr(net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 0})
		// This may look weird, but this is intended. We don't need this to be set for the caller, only for
		// the rest of this function's scope.
		//noinspection GoAssignmentToReceiver
		addr = &v
	}
	var ver byte = 6
	if addr.IP.To4() != nil {
		ver = 4
	}

	// Write the version byte first.
	if err := buffer.WriteByte(ver); err != nil {
		return nil, fmt.Errorf("error writing raknet address version byte: %v", err)
	}

	if ver == 4 {
		ipBytes := addr.IP.To4()

		// If the IP is an IPv4 IP, we write all 4 bytes individually.
		if _, err := buffer.Write([]byte{ipBytes[0], ^ipBytes[1], ^ipBytes[2], ^ipBytes[3]}); err != nil {
			return nil, fmt.Errorf("error writing raknet address ipv4 bytes: %v", err)
		}
		// Finally write the port.
		if err := binary.Write(buffer, binary.BigEndian, int16(addr.Port)); err != nil {
			return nil, fmt.Errorf("error writing raknet address port: %v", err)
		}
	} else {
		if err := binary.Write(buffer, binary.LittleEndian, int16(23)); err != nil {
			return nil, fmt.Errorf("error writing raknet address port: %v", err)
		}
		if err := binary.Write(buffer, binary.BigEndian, int16(addr.Port)); err != nil {
			return nil, fmt.Errorf("error writing raknet address port: %v", err)
		}
		// The IPv6 address is enclosed in two 0 integers, which we represent with an empty 4 length byte
		// slice.
		if _, err := buffer.Write(append(make([]byte, 4), append(addr.IP, make([]byte, 4)...)...)); err != nil {
			return nil, fmt.Errorf("error writing raknet address ipv6 bytes: %v", err)
		}
	}
	return buffer.Bytes(), nil
}
