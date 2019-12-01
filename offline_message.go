package raknet

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	idUnconnectedPing byte = 0x01
	idUnconnectedPong byte = 0x1c

	idOpenConnectionRequest1 byte = 0x05
	idOpenConnectionReply1   byte = 0x06
	idOpenConnectionRequest2 byte = 0x07
	idOpenConnectionReply2   byte = 0x08

	idIncompatibleProtocolVersion byte = 0x19
)

var magic = [16]byte{
	0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78,
}

type unconnectedPing struct {
	SendTimestamp int64
	Magic         [16]byte
	ClientGUID    int64
}

type unconnectedPong struct {
	SendTimestamp int64
	ServerGUID    int64
	Magic         [16]byte
}

type openConnectionRequest1 struct {
	Magic    [16]byte
	Protocol byte
}

type openConnectionReply1 struct {
	Magic      [16]byte
	ServerGUID int64
	Secure     bool
	MTUSize    int16
}

type incompatibleProtocolVersion struct {
	ServerProtocol byte
	Magic          [16]byte
	ServerGUID     int64
}

type openConnectionRequest2 struct {
	Magic         [16]byte
	ServerAddress *rakAddr
	MTUSize       int16
	ClientGUID    int64
}

// MarshalBinary converts an open connection request 2 to its binary representation.
func (request *openConnectionRequest2) MarshalBinary() (b []byte, err error) {
	addrBytes, err := request.ServerAddress.MarshalBinary()
	if err != nil {
		return nil, err
	}
	buffer := bytes.NewBuffer(append(magic[:], addrBytes...))
	if err := binary.Write(buffer, binary.BigEndian, request.MTUSize); err != nil {
		return nil, err
	}
	if err := binary.Write(buffer, binary.BigEndian, request.ClientGUID); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// UnmarshalBinary parses a binary representation of an open connection request 2.
func (request *openConnectionRequest2) UnmarshalBinary(b []byte) error {
	buffer := bytes.NewBuffer(b)
	// Skip the magic.
	buffer.Next(16)

	addr, err := unmarshalAddr(buffer)
	if err != nil {
		return err
	}
	request.ServerAddress = addr
	if err := binary.Read(buffer, binary.BigEndian, &request.MTUSize); err != nil {
		return err
	}
	if err := binary.Read(buffer, binary.BigEndian, &request.ClientGUID); err != nil {
		return err
	}
	return nil
}

type openConnectionReply2 struct {
	Magic         [16]byte
	ServerGUID    int64
	ClientAddress *rakAddr
	MTUSize       int16
	Secure        bool
}

// MarshalBinary converts an open connection reply 2 to its binary representation.
func (reply *openConnectionReply2) MarshalBinary() (b []byte, err error) {
	buffer := bytes.NewBuffer(magic[:])
	if err := binary.Write(buffer, binary.BigEndian, reply.ServerGUID); err != nil {
		return nil, err
	}
	addrBytes, err := reply.ClientAddress.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if _, err := buffer.Write(addrBytes); err != nil {
		return nil, err
	}
	if err := binary.Write(buffer, binary.BigEndian, reply.MTUSize); err != nil {
		return nil, err
	}
	var secure byte
	if reply.Secure {
		secure = 1
	}
	if err := buffer.WriteByte(secure); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// UnmarshalBinary decode a serialised open connection reply 2 into a struct.
func (reply *openConnectionReply2) UnmarshalBinary(b []byte) error {
	buffer := bytes.NewBuffer(b)
	// Skip the magic sequence.
	buffer.Next(16)
	if err := binary.Read(buffer, binary.BigEndian, &reply.ServerGUID); err != nil {
		return err
	}
	addr, err := unmarshalAddr(buffer)
	if err != nil {
		return err
	}
	reply.ClientAddress = addr

	if err := binary.Read(buffer, binary.BigEndian, &reply.MTUSize); err != nil {
		return err
	}
	if err := binary.Read(buffer, binary.BigEndian, &reply.Secure); err != nil {
		return err
	}
	return nil
}

// unmarshalAddr decodes a RakNet address from the buffer passed. If not successful, an error is returned.
func unmarshalAddr(buffer *bytes.Buffer) (*rakAddr, error) {
	addr := &rakAddr{}
	ver, err := buffer.ReadByte()
	if err != nil {
		return nil, err
	}
	_ = buffer.UnreadByte()
	var addrBytes []byte
	if ver == 4 {
		addrBytes = buffer.Next(ipv4AddrSize)
		if len(addrBytes) != ipv4AddrSize {
			return nil, fmt.Errorf("not enough bytes for ipv4 address")
		}
	} else {
		addrBytes = buffer.Next(ipv6AddrSize)
		if len(addrBytes) != ipv6AddrSize {
			return nil, fmt.Errorf("not enough bytes for ipv6 address")
		}
	}
	if err := addr.UnmarshalBinary(addrBytes); err != nil {
		return nil, err
	}
	return addr, nil
}
