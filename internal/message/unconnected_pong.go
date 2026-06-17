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
	// need at least ping(8)+serverGUID(8)+magic(16) = 32 bytes
	if len(data) < 32 {
		return io.ErrUnexpectedEOF
	}
	pk.PingTime = int64(binary.BigEndian.Uint64(data))
	pk.ServerGUID = int64(binary.BigEndian.Uint64(data[8:]))

	// if there are fewer than 34 bytes then length field is absent -> no Data
	if len(data) < 34 {
		pk.Data = nil
		return nil
	}

	// read length and validate remaining bytes
	n := int(binary.BigEndian.Uint16(data[32:34]))
	if len(data) < 34+n {
		return io.ErrUnexpectedEOF
	}
	if n == 0 {
		pk.Data = nil
		return nil
	}

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
