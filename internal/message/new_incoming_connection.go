package message

import (
	"bytes"
	"encoding/binary"
	"net"
)

type NewIncomingConnection struct {
	ServerAddress     net.UDPAddr
	SystemAddresses   [20]net.UDPAddr
	RequestTimestamp  int64
	AcceptedTimestamp int64
}

func (pk *NewIncomingConnection) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDNewIncomingConnection)
	writeAddr(buf, pk.ServerAddress)
	for _, addr := range pk.SystemAddresses {
		writeAddr(buf, addr)
	}
	_ = binary.Write(buf, binary.BigEndian, pk.RequestTimestamp)
	_ = binary.Write(buf, binary.BigEndian, pk.AcceptedTimestamp)
}

func (pk *NewIncomingConnection) Read(buf *bytes.Buffer) error {
	_ = readAddr(buf, &pk.ServerAddress)
	for i := 0; i < 20; i++ {
		_ = readAddr(buf, &pk.SystemAddresses[i])
		if buf.Len() == 16 {
			break
		}
	}
	_ = binary.Read(buf, binary.BigEndian, &pk.RequestTimestamp)
	return binary.Read(buf, binary.BigEndian, &pk.AcceptedTimestamp)
}
