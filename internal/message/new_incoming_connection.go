package message

import (
	"encoding/binary"
	"io"
	"net/netip"
)

type NewIncomingConnection struct {
	ServerAddress     netip.AddrPort
	SystemAddresses   systemAddresses
	RequestTimestamp  int64
	AcceptedTimestamp int64
}

func (pk *NewIncomingConnection) UnmarshalBinary(data []byte) error {
	if len(data) < addrSize(data) {
		return io.ErrUnexpectedEOF
	}
	var offset int
	pk.ServerAddress, offset = addr(data)
	for i := range 20 {
		if len(data) < addrSize(data[offset:]) {
			return io.ErrUnexpectedEOF
		}
		address, n := addr(data[offset:])
		pk.SystemAddresses[i] = address
		offset += n

		if len(data[offset:]) == 16 {
			// Some implementations send only 10 system addresses.
			break
		}
	}
	if len(data[offset:]) < 16 {
		return io.ErrUnexpectedEOF
	}
	pk.RequestTimestamp = int64(binary.BigEndian.Uint64(data[offset:]))
	pk.AcceptedTimestamp = int64(binary.BigEndian.Uint64(data[offset+8:]))
	return nil
}

func (pk *NewIncomingConnection) MarshalBinary() (data []byte, err error) {
	nAddr, nSys := sizeofAddr(pk.ServerAddress), pk.SystemAddresses.sizeOf()
	b := make([]byte, 1+nAddr+nSys+16)
	b[0] = IDNewIncomingConnection
	offset := 1 + putAddr(b[1:], pk.ServerAddress)
	for _, addr := range pk.SystemAddresses {
		offset += putAddr(b[offset:], addr)
	}
	binary.BigEndian.PutUint64(b[offset:], uint64(pk.RequestTimestamp))
	binary.BigEndian.PutUint64(b[offset+8:], uint64(pk.AcceptedTimestamp))
	return b, nil
}
