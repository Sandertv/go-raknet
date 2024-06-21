package message

import (
	"encoding/binary"
	"io"
	"net/netip"
)

type ConnectionRequestAccepted struct {
	ClientAddress   netip.AddrPort
	SystemIndex     uint16
	SystemAddresses systemAddresses
	// PingTime is filled out with ConnectionRequest.RequestTime.
	PingTime int64
	// PongTime is a timestamp from the moment the packet is sent.
	PongTime int64
}

func (pk *ConnectionRequestAccepted) UnmarshalBinary(data []byte) error {
	if len(data) < addrSize(data) {
		return io.ErrUnexpectedEOF
	}
	var offset int
	pk.ClientAddress, offset = addr(data)
	pk.SystemIndex = binary.BigEndian.Uint16(data[offset:])
	offset += 2
	for i := range 20 {
		if len(data[offset:]) == 16 {
			// Some implementations send fewer system addresses.
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

func (pk *ConnectionRequestAccepted) MarshalBinary() (data []byte, err error) {
	nAddr, nSys := sizeofAddr(pk.ClientAddress), pk.SystemAddresses.sizeOf()
	b := make([]byte, 1+nAddr+2+nSys+16)
	b[0] = IDConnectionRequestAccepted
	offset := 1 + putAddr(b[1:], pk.ClientAddress)
	binary.BigEndian.PutUint16(b[offset:], pk.SystemIndex)
	for _, addr := range pk.SystemAddresses {
		offset += putAddr(b[offset+2:], addr)
	}
	binary.BigEndian.PutUint64(b[offset+2:], uint64(pk.PingTime))
	binary.BigEndian.PutUint64(b[offset+10:], uint64(pk.PongTime))
	return b, nil
}
