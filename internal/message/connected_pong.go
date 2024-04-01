package message

import (
	"encoding/binary"
	"io"
)

type ConnectedPong struct {
	ClientTimestamp int64
	ServerTimestamp int64
}

func (pk *ConnectedPong) UnmarshalBinary(data []byte) error {
	if len(data) < 16 {
		return io.ErrUnexpectedEOF
	}
	pk.ClientTimestamp = int64(binary.BigEndian.Uint64(data))
	pk.ServerTimestamp = int64(binary.BigEndian.Uint64(data[8:]))
	return nil
}

func (pk *ConnectedPong) MarshalBinary() (data []byte, err error) {
	b := make([]byte, 17)
	b[0] = IDConnectedPong
	binary.BigEndian.PutUint64(b[1:], uint64(pk.ClientTimestamp))
	binary.BigEndian.PutUint64(b[9:], uint64(pk.ServerTimestamp))
	return b, nil
}
