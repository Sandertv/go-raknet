package message

import (
	"bytes"
	"encoding/binary"
)

type ConnectionRequest struct {
	ClientGUID       int64
	RequestTimestamp int64
	Secure           bool
}

func (pk *ConnectionRequest) Write(buf *bytes.Buffer) {
	_ = binary.Write(buf, binary.BigEndian, IDConnectionRequest)
	_ = binary.Write(buf, binary.BigEndian, pk.ClientGUID)
	_ = binary.Write(buf, binary.BigEndian, pk.RequestTimestamp)
	_ = binary.Write(buf, binary.BigEndian, pk.Secure)
}

func (pk *ConnectionRequest) Read(buf *bytes.Buffer) error {
	_ = binary.Read(buf, binary.BigEndian, &pk.ClientGUID)
	_ = binary.Read(buf, binary.BigEndian, &pk.RequestTimestamp)
	return binary.Read(buf, binary.BigEndian, &pk.Secure)
}
