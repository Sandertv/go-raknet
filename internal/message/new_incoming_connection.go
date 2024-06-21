package message

import (
	"encoding/binary"
	"io"
	"net/netip"
)

type NewIncomingConnection struct {
	ServerAddress   netip.AddrPort
	SystemAddresses systemAddresses
	// PingTime is filled out with ConnectionRequestAccepted.PongTime.
	PingTime int64
	// PongTime is a timestamp from the moment the packet is sent.
	PongTime int64
}

func (pk *NewIncomingConnection) UnmarshalBinary(data []byte) error {
	if len(data) < addrSize(data) {
		return io.ErrUnexpectedEOF
	}
	var offset int
	pk.ServerAddress, offset = addr(data)
	for i := range 20 {
		if len(data[offset:]) == 16 {
			// Some implementations send only 10 system addresses.
			break
		}
		if len(data[offset:]) < addrSize(data[offset:]) {
			return io.ErrUnexpectedEOF
		}
		address, n := addr(data[offset:])
		pk.SystemAddresses[i] = address
		offset += n
	}
	if len(data[offset:]) < 16 {
		return io.ErrUnexpectedEOF
	}
	pk.PingTime = int64(binary.BigEndian.Uint64(data[offset:]))
	pk.PongTime = int64(binary.BigEndian.Uint64(data[offset+8:]))
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
	binary.BigEndian.PutUint64(b[offset:], uint64(pk.PingTime))
	binary.BigEndian.PutUint64(b[offset+8:], uint64(pk.PongTime))
	return b, nil
}
