package message

import (
	"encoding/binary"
	"io"
)

type IncompatibleProtocolVersion struct {
	ServerProtocol byte
	ServerGUID     int64
}

func (pk *IncompatibleProtocolVersion) UnmarshalBinary(data []byte) error {
	if len(data) < 25 {
		return io.ErrUnexpectedEOF
	}
	pk.ServerProtocol = data[0]
	// Magic: 16 bytes
	pk.ServerGUID = int64(binary.BigEndian.Uint64(data[17:]))
	return nil
}

func (pk *IncompatibleProtocolVersion) MarshalBinary() (data []byte, err error) {
	b := make([]byte, 26)
	b[0] = IDIncompatibleProtocolVersion
	b[1] = pk.ServerProtocol
	copy(b[2:], unconnectedMessageSequence[:])
	binary.BigEndian.PutUint64(b[18:], uint64(pk.ServerGUID))
	return b, nil
}
