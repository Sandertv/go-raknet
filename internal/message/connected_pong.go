package message

import (
	"bytes"
	"encoding/binary"
)

type ConnectedPong struct {
	ClientTimestamp int64
	ServerTimestamp int64
}

func (pk *ConnectedPong) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDConnectedPong)
	_ = binary.Write(buf, binary.BigEndian, pk.ClientTimestamp)
	_ = binary.Write(buf, binary.BigEndian, pk.ServerTimestamp)
}

func (pk *ConnectedPong) Read(buf *bytes.Buffer) error {
	_ = binary.Read(buf, binary.BigEndian, &pk.ClientTimestamp)
	return binary.Read(buf, binary.BigEndian, &pk.ServerTimestamp)
}
