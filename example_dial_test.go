package raknet_test

import (
	"fmt"
	"github.com/sandertv/go-raknet"
)

func ExamplePing() {
	const address = "mco.mineplex.com:19132"

	// Ping the target address. This will ping with a timeout of 5 seconds. raknet.PingContext and
	// raknet.PingTimeout may be used to cancel at any other time.
	data, err := raknet.Ping(address)
	if err != nil {
		panic("error pinging " + address + ": " + err.Error())
	}
	str := string(data)

	fmt.Println(str[:4])
	// Output: MCPE
}

func ExampleDial() {
	const address = "mco.mineplex.com:19132"

	// Dial a connection to the target address. This will time out after up to 10 seconds. raknet.DialTimeout
	// and raknet.DialContext may be used to cancel at any other time.
	conn, err := raknet.Dial(address)
	if err != nil {
		panic("error connecting to " + address + ": " + err.Error())
	}

	// Read a packet from the connection dialed.
	p := make([]byte, 1500)
	n, err := conn.Read(p)
	if err != nil {
		panic("error reading packet from " + address + ": " + err.Error())
	}
	p = p[:n]

	// Write a packet to the connection.
	data := []byte("Hello World!")
	if _, err := conn.Write(data); err != nil {
		panic("error writing packet to " + address + ": " + err.Error())
	}

	// Close the connection after you're done with it.
	if err := conn.Close(); err != nil {
		panic("error closing connection: " + err.Error())
	}
}
