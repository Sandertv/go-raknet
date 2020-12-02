package message

import (
	"bytes"
	"encoding/binary"
	"net"
)

type OpenConnectionReply2 struct {
	Magic         [16]byte
	ServerGUID    int64
	ClientAddress net.UDPAddr
	MTUSize       uint16
	Secure        bool
}

func (pk *OpenConnectionReply2) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDOpenConnectionReply2)
	_ = binary.Write(buf, binary.BigEndian, unconnectedMessageSequence)
	_ = binary.Write(buf, binary.BigEndian, pk.ServerGUID)
	writeAddr(buf, pk.ClientAddress)
	_ = binary.Write(buf, binary.BigEndian, pk.MTUSize)
	_ = binary.Write(buf, binary.BigEndian, pk.Secure)
}

func (pk *OpenConnectionReply2) Read(buf *bytes.Buffer) error {
	_ = binary.Read(buf, binary.BigEndian, &pk.Magic)
	_ = binary.Read(buf, binary.BigEndian, &pk.ServerGUID)
	_ = readAddr(buf, &pk.ClientAddress)
	_ = binary.Read(buf, binary.BigEndian, &pk.MTUSize)
	return binary.Read(buf, binary.BigEndian, &pk.Secure)
}
