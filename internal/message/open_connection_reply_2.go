package message

import (
	"encoding/binary"
	"io"
	"net/netip"
)

type OpenConnectionReply2 struct {
	ServerGUID    int64
	ClientAddress netip.AddrPort
	MTU           uint16
	DoSecurity    bool
}

func (pk *OpenConnectionReply2) UnmarshalBinary(data []byte) error {
	if len(data) < 24 || len(data) < 27+addrSize(data[24:]) {
		return io.ErrUnexpectedEOF
	}
	// Magic: 16 bytes.
	pk.ServerGUID = int64(binary.BigEndian.Uint64(data[16:]))
	pk.ClientAddress, _ = addr(data[24:])
	offset := addrSize(data[24:])
	pk.MTU = binary.BigEndian.Uint16(data[24+offset:])
	pk.DoSecurity = data[26+offset] != 0

	return nil
}

func (pk *OpenConnectionReply2) MarshalBinary() (data []byte, err error) {
	offset := sizeofAddr(pk.ClientAddress)
	b := make([]byte, 28+offset)
	b[0] = IDOpenConnectionReply2
	copy(b[1:], unconnectedMessageSequence[:])
	binary.BigEndian.PutUint64(b[17:], uint64(pk.ServerGUID))
	putAddr(b[25:], pk.ClientAddress)
	binary.BigEndian.PutUint16(b[25+offset:], pk.MTU)
	if pk.DoSecurity {
		b[27+offset] = 1
	}
	return b, nil
}
