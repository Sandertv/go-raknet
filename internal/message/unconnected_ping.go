package message

import (
	"encoding/binary"
	"io"
)

type UnconnectedPing struct {
	PingTime   int64
	ClientGUID int64
}

func (pk *UnconnectedPing) MarshalBinary() (data []byte, err error) {
	b := make([]byte, 33)
	b[0] = IDUnconnectedPing
	binary.BigEndian.PutUint64(b[1:], uint64(pk.PingTime))
	copy(b[9:], unconnectedMessageSequence[:])
	binary.BigEndian.PutUint64(b[25:], uint64(pk.ClientGUID))
	return b, nil
}

func (pk *UnconnectedPing) UnmarshalBinary(data []byte) error {
	if len(data) < 32 {
		return io.ErrUnexpectedEOF
	}
	pk.PingTime = int64(binary.BigEndian.Uint64(data))
	// Magic: 16 bytes.
	pk.ClientGUID = int64(binary.BigEndian.Uint64(data[24:]))
	return nil
}
