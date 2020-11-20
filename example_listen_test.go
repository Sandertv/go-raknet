package raknet_test

import (
	"github.com/sandertv/go-raknet"
)

func ExampleListen() {
	const address = ":19132"

	// Start listening on an address.
	l, err := raknet.Listen(address)
	if err != nil {
		panic(err)
	}

	for {
		// Accept a new connection from the Listener. Accept will only return an error if the Listener is
		// closed. (So only after a call to Listener.Close.)
		conn, err := l.Accept()
		if err != nil {
			return
		}

		// Read a packet from the connection accepted.
		p := make([]byte, 1500)
		n, err := conn.Read(p)
		if err != nil {
			panic("error reading packet from " + conn.RemoteAddr().String() + ": " + err.Error())
		}
		p = p[:n]

		// Write a packet to the connection.
		data := []byte("Hello World!")
		if _, err := conn.Write(data); err != nil {
			panic("error writing packet to " + conn.RemoteAddr().String() + ": " + err.Error())
		}

		// Close the connection after you're done with it.
		if err := conn.Close(); err != nil {
			panic("error closing connection: " + err.Error())
		}
	}
}
