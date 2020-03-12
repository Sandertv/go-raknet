package message

import (
	"bytes"
	"encoding/binary"
)

type IncompatibleProtocolVersion struct {
	Magic          [16]byte
	ServerProtocol byte
	ServerGUID     int64
}

func (pk *IncompatibleProtocolVersion) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDIncompatibleProtocolVersion)
	_ = binary.Write(buf, binary.BigEndian, pk.ServerProtocol)
	_ = binary.Write(buf, binary.BigEndian, unconnectedMessageSequence)
	_ = binary.Write(buf, binary.BigEndian, pk.ServerGUID)
}

func (pk *IncompatibleProtocolVersion) Read(buf *bytes.Buffer) error {
	_ = binary.Read(buf, binary.BigEndian, &pk.ServerProtocol)
	_ = binary.Read(buf, binary.BigEndian, &pk.Magic)
	return binary.Read(buf, binary.BigEndian, &pk.ServerGUID)
}
