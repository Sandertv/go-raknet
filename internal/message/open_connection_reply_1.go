package message

import (
	"bytes"
	"encoding/binary"
)

type OpenConnectionReply1 struct {
	Magic      [16]byte
	ServerGUID int64
	Secure     bool
	MTUSize    int16
}

func (pk *OpenConnectionReply1) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDOpenConnectionReply1)
	_ = binary.Write(buf, binary.BigEndian, unconnectedMessageSequence)
	_ = binary.Write(buf, binary.BigEndian, pk.ServerGUID)
	_ = binary.Write(buf, binary.BigEndian, pk.Secure)
	_ = binary.Write(buf, binary.BigEndian, pk.MTUSize)
}

func (pk *OpenConnectionReply1) Read(buf *bytes.Buffer) error {
	_ = binary.Read(buf, binary.BigEndian, &pk.Magic)
	_ = binary.Read(buf, binary.BigEndian, &pk.ServerGUID)
	_ = binary.Read(buf, binary.BigEndian, &pk.Secure)
	return binary.Read(buf, binary.BigEndian, &pk.MTUSize)
}
