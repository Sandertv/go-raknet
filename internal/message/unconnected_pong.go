package message

import (
	"encoding/binary"
	"io"
)

type UnconnectedPong struct {
	// PingTime is filled out using UnconnectedPing.PingTime.
	PingTime   int64
	ServerGUID int64
	Data       []byte
}

func (pk *UnconnectedPong) UnmarshalBinary(data []byte) error {
	if len(data) < 34 || len(data) < 34+int(binary.BigEndian.Uint16(data[32:])) {
		return io.ErrUnexpectedEOF
	}
	pk.PingTime = int64(binary.BigEndian.Uint64(data))
	pk.ServerGUID = int64(binary.BigEndian.Uint64(data[8:]))
	// Magic: 16 bytes.
	n := binary.BigEndian.Uint16(data[32:])
	pk.Data = append([]byte(nil), data[34:34+n]...)
	return nil
}

func (pk *UnconnectedPong) MarshalBinary() (data []byte, err error) {
	b := make([]byte, 35+len(pk.Data))
	b[0] = IDUnconnectedPong
	binary.BigEndian.PutUint64(b[1:], uint64(pk.PingTime))
	binary.BigEndian.PutUint64(b[9:], uint64(pk.ServerGUID))
	copy(b[17:], unconnectedMessageSequence[:])
	binary.BigEndian.PutUint16(b[33:], uint16(len(pk.Data)))
	copy(b[35:], pk.Data)
	return b, nil
}
