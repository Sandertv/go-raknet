package message

import (
	"bytes"
	"encoding/binary"
)

type UnconnectedPing struct {
	Magic         [16]byte
	SendTimestamp int64
	ClientGUID    int64
}

func (pk *UnconnectedPing) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDUnconnectedPing)
	_ = binary.Write(buf, binary.BigEndian, pk.SendTimestamp)
	_ = binary.Write(buf, binary.BigEndian, unconnectedMessageSequence)
	_ = binary.Write(buf, binary.BigEndian, pk.ClientGUID)
}

func (pk *UnconnectedPing) Read(buf *bytes.Buffer) error {
	_ = binary.Read(buf, binary.BigEndian, &pk.SendTimestamp)
	_ = binary.Read(buf, binary.BigEndian, &pk.Magic)
	return binary.Read(buf, binary.BigEndian, &pk.ClientGUID)
}
