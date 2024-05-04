package message

import (
	"encoding/binary"
	"io"
)

type ConnectionRequest struct {
	ClientGUID int64
	// RequestTime is a timestamp from the moment the packet is sent.
	RequestTime int64
	Secure      bool
}

func (pk *ConnectionRequest) UnmarshalBinary(data []byte) error {
	if len(data) < 17 {
		return io.ErrUnexpectedEOF
	}
	pk.ClientGUID = int64(binary.BigEndian.Uint64(data))
	pk.RequestTime = int64(binary.BigEndian.Uint64(data[8:]))
	pk.Secure = data[16] != 0
	return nil
}

func (pk *ConnectionRequest) MarshalBinary() (data []byte, err error) {
	b := make([]byte, 18)
	b[0] = IDConnectionRequest
	binary.BigEndian.PutUint64(b[1:], uint64(pk.ClientGUID))
	binary.BigEndian.PutUint64(b[9:], uint64(pk.RequestTime))
	if pk.Secure {
		b[17] = 1
	}
	return b, nil
}
