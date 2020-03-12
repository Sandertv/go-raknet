package message

import (
	"bytes"
	"encoding/binary"
	"net"
)

type ConnectionRequestAccepted struct {
	ClientAddress     net.UDPAddr
	SystemAddresses   [20]net.UDPAddr
	RequestTimestamp  int64
	AcceptedTimestamp int64
}

func (pk *ConnectionRequestAccepted) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDConnectionRequestAccepted)
	writeAddr(buf, pk.ClientAddress)
	_ = binary.Write(buf, binary.BigEndian, int16(0))
	for _, addr := range pk.SystemAddresses {
		writeAddr(buf, addr)
	}
	_ = binary.Write(buf, binary.BigEndian, pk.RequestTimestamp)
	_ = binary.Write(buf, binary.BigEndian, pk.AcceptedTimestamp)
}

func (pk *ConnectionRequestAccepted) Read(buf *bytes.Buffer) error {
	_ = readAddr(buf, &pk.ClientAddress)
	buf.Next(2)
	for i := 0; i < 20; i++ {
		_ = readAddr(buf, &pk.SystemAddresses[i])
		if buf.Len() == 16 {
			break
		}
	}
	_ = binary.Read(buf, binary.BigEndian, &pk.RequestTimestamp)
	return binary.Read(buf, binary.BigEndian, &pk.AcceptedTimestamp)
}
