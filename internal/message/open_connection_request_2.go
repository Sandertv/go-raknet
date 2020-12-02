package message

import (
	"bytes"
	"encoding/binary"
	"net"
)

type OpenConnectionRequest2 struct {
	Magic                  [16]byte
	ServerAddress          net.UDPAddr
	ClientPreferredMTUSize uint16
	ClientGUID             int64
}

func (pk *OpenConnectionRequest2) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDOpenConnectionRequest2)
	_ = binary.Write(buf, binary.BigEndian, unconnectedMessageSequence)
	writeAddr(buf, pk.ServerAddress)
	_ = binary.Write(buf, binary.BigEndian, pk.ClientPreferredMTUSize)
	_ = binary.Write(buf, binary.BigEndian, pk.ClientGUID)
}

func (pk *OpenConnectionRequest2) Read(buf *bytes.Buffer) error {
	_ = binary.Read(buf, binary.BigEndian, &pk.Magic)
	_ = readAddr(buf, &pk.ServerAddress)
	_ = binary.Read(buf, binary.BigEndian, &pk.ClientPreferredMTUSize)
	return binary.Read(buf, binary.BigEndian, &pk.ClientGUID)
}
