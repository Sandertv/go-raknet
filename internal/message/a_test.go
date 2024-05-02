package message

import (
	"encoding/hex"
	"fmt"
	"testing"
)

func TestNewIncomingConnection_MarshalBinary(t *testing.T) {
	fmt.Println(addr([]byte{0x04, 0x53, 0xea, 0xaf, 0xfe, 0xcd, 0xb7}))
	b, _ := hex.DecodeString("061700cdb700000000fe800000000000004f0e3cf91ffb6fec18000000")
	fmt.Println(addr(b))

	// // - 06 17 00 cd b7 00 00 00 00 fe 80 00 00 00 00 00 00 4f 0e 3c f9 1f fb 6f ec 18 00 00 00
	// // - 06 17 00 cd b7 00 00 00 00 fe 80 00 00 00 00 00 00 41 3b ba 6a 05 eb 1c 00 10 00 00 00
	// // - 06 17 00 cd b7 00 00 00 00 fe 80 00 00 00 00 00 00 31 2e 2f f9 0e 56 f5 51 0d 00 00 00
	// // - 06 17 00 cd b7 00 00 00 00 fe 80 00 00 00 00 00 00 15 f1 39 7f 5a b6 43 9e 42 00 00 00
	// // - 04 53 ea af fe cd b7
	// // - 04 9b b9 94 99 cd b7
	// // - 04 53 ed 8f fe cd b7
	// // - 04 3f 57 ff 8e cd b7
}
