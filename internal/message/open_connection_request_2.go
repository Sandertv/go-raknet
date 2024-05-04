package message

import (
	"encoding/binary"
	"io"
	"net/netip"
)

type OpenConnectionRequest2 struct {
	ServerAddress     netip.AddrPort
	MTU               uint16
	ClientGUID        int64
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
	if len(data) < 16 || len(data) < 26+addrSize(data[16:]) {
		return io.ErrUnexpectedEOF
	}
	// Magic: 16 bytes.
	offset := addrSize(data[16:])
	pk.ServerAddress, _ = addr(data[16:])
	pk.MTU = binary.BigEndian.Uint16(data[16+offset:])
	pk.ClientGUID = int64(binary.BigEndian.Uint64(data[18+offset:]))
	return nil
}
