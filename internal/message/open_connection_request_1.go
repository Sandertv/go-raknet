package message

import (
	"bytes"
	"encoding/binary"
)

type OpenConnectionRequest1 struct {
	Magic    [16]byte
	Protocol byte
	MTUSize  int16
}

func (pk *OpenConnectionRequest1) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDOpenConnectionRequest1)
	_ = binary.Write(buf, binary.BigEndian, unconnectedMessageSequence)
	_ = binary.Write(buf, binary.BigEndian, pk.Protocol)
	_, _ = buf.Write(make([]byte, pk.MTUSize-int16(buf.Len())-28))
}

func (pk *OpenConnectionRequest1) Read(buf *bytes.Buffer) error {
	pk.MTUSize = int16(buf.Len() + 1)
	_ = binary.Read(buf, binary.BigEndian, &pk.Magic)
	return binary.Read(buf, binary.BigEndian, &pk.Protocol)
}
