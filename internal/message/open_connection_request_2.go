package message

import (
	"encoding/binary"
	"io"
	"net/netip"
)

type OpenConnectionRequest2 struct {
	ServerAddress netip.AddrPort
	MTU           uint16
	ClientGUID    int64
	// ServerHasSecurity specifies if the server has security enabled (and thus
	// a cookie must be sent in this packet). This field is NOT written in this
	// packet, so it must be set appropriately even when calling
	// UnmarshalBinary.
	ServerHasSecurity bool
	Cookie            uint32
}

func (pk *OpenConnectionRequest2) MarshalBinary() (data []byte, err error) {
	cookieOffset := 0
	offset := sizeofAddr(pk.ServerAddress)
	if pk.ServerHasSecurity {
		cookieOffset += 5
	}
	b := make([]byte, 27+offset+cookieOffset)
	b[0] = IDOpenConnectionRequest2
	copy(b[1:], unconnectedMessageSequence[:])
	if pk.ServerHasSecurity {
		binary.BigEndian.PutUint32(b[17:], pk.Cookie)
	}
	putAddr(b[17+cookieOffset:], pk.ServerAddress)
	binary.BigEndian.PutUint16(b[17+offset+cookieOffset:], pk.MTU)
	binary.BigEndian.PutUint64(b[19+offset+cookieOffset:], uint64(pk.ClientGUID))

	return b, nil
}

func (pk *OpenConnectionRequest2) UnmarshalBinary(data []byte) error {
	cookieOffset := 0
	if pk.ServerHasSecurity {
		cookieOffset = 5
	}
	if len(data) < 16+cookieOffset || len(data) < 26+cookieOffset+addrSize(data[16+cookieOffset:]) {
		return io.ErrUnexpectedEOF
	}
	// Magic: 16 bytes.
	if pk.ServerHasSecurity {
		pk.Cookie = binary.BigEndian.Uint32(data[16:])
	}
	offset := cookieOffset
	var n int
	pk.ServerAddress, n = addr(data[16+offset:])
	offset += n
	pk.MTU = binary.BigEndian.Uint16(data[16+offset:])
	pk.ClientGUID = int64(binary.BigEndian.Uint64(data[18+offset:]))
	return nil
}
