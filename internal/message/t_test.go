package message

import (
	"encoding/hex"
	"log"
	"testing"
)

func TestIndian(t *testing.T) {
	b, _ := hex.DecodeString("043f57ff834abc043f57ff73e74f04ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000004ffffffff000006000000170000000000000005f5b9a5")
	log.Print(addr(b))
	log.Print(b)
}
