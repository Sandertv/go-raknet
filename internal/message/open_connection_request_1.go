package message

import (
	"io"
)

type OpenConnectionRequest1 struct {
	Protocol              byte
	MaximumSizeNotDropped uint16
}

var cachedOCR1 = map[uint16][]byte{}

func (pk *OpenConnectionRequest1) MarshalBinary() (data []byte, err error) {
	if b, ok := cachedOCR1[pk.MaximumSizeNotDropped]; ok {
		// Cache OpenConnectionRequest1 data. These are independent of any other
		// inputs and are pretty big.
		return b, nil
	}
	b := make([]byte, pk.MaximumSizeNotDropped-20-8) // IP Header: 20 bytes, UDP Header: 8 bytes.
	b[0] = IDOpenConnectionRequest1
	copy(b[1:], unconnectedMessageSequence[:])
	b[17] = pk.Protocol

	cachedOCR1[pk.MaximumSizeNotDropped] = b
	return b, nil
}

func (pk *OpenConnectionRequest1) UnmarshalBinary(data []byte) error {
	if len(data) < 17 {
		return io.ErrUnexpectedEOF
	}
	// Magic: 16 bytes.
	pk.Protocol = data[16]
	pk.MaximumSizeNotDropped = uint16(len(data) + 20 + 8 + 1) // Headers + packet ID.
	return nil
}
