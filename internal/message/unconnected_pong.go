package message

import (
	"bytes"
	"encoding/binary"
)

type UnconnectedPong struct {
	Magic         [16]byte
	SendTimestamp int64
	ServerGUID    int64
	Data          []byte
}

func (pk *UnconnectedPong) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDUnconnectedPong)
	_ = binary.Write(buf, binary.BigEndian, pk.SendTimestamp)
	_ = binary.Write(buf, binary.BigEndian, pk.ServerGUID)
	_ = binary.Write(buf, binary.BigEndian, unconnectedMessageSequence)
	_ = binary.Write(buf, binary.BigEndian, int16(len(pk.Data)))
	_ = binary.Write(buf, binary.BigEndian, pk.Data)
}

func (pk *UnconnectedPong) Read(buf *bytes.Buffer) error {
	var l int16
	_ = binary.Read(buf, binary.BigEndian, &pk.SendTimestamp)
	_ = binary.Read(buf, binary.BigEndian, &pk.ServerGUID)
	_ = binary.Read(buf, binary.BigEndian, &pk.Magic)
	_ = binary.Read(buf, binary.BigEndian, &l)
	pk.Data = make([]byte, l)
	_, err := buf.Read(pk.Data)
	return err
}
