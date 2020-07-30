package main

import (
	"log"

	"github.com/sandertv/go-raknet"
)

func main() {
	// First we do a quick connection to get server's MTU and reuse with clients.
	// Since this is an UDP raw proxy we don't reassemble packets. MTUs are
	// negotiated between server and clients at the raknet connection handshake,
	// where this handshake is actually not forwarded, but re-established by the
	// proxy, therefore we need to match MTU between server and clients ourselves.
	server, err := raknet.Dial("mco.mineplex.com:19132")
	if err != nil {
		panic(err)
	}
	mtu := server.MtuSize()
	server.Close()

	listener, err := raknet.ListenWithMaxMtu("0.0.0.0:19132", mtu)
	defer func() {
		_ = listener.Close()
	}()
	if err != nil {
		panic(err)
	}
	// We hijack the pong of a Minecraft server, so our proxy will continuously send the pong data of the
	// server.
	//noinspection SpellCheckingInspection
	if err := listener.HijackPong("mco.mineplex.com:19132"); err != nil {
		panic(err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		// We spin up a new connection with the server each time a client connects to the proxy.
		// noinspection SpellCheckingInspection. Limit mtu to match client side mtu.
		server, err := raknet.DialWithMaxMtu("mco.mineplex.com:19132", conn.MtuSize())
		if err != nil {
			panic(err)
		}
		go func() {
			b := make([]byte, 300000)
			for {
				n, err := conn.Read(b)
				if err != nil {
					if !raknet.ErrConnectionClosed(err) {
						log.Printf("error reading from client connection: %v\n", err)
					}
					_ = server.Close()
					return
				}
				packet := b[:n]
				if _, err := server.Write(packet); err != nil {
					if !raknet.ErrConnectionClosed(err) {
						log.Printf("error writing to server connection: %v\n", err)
					}
					_ = conn.Close()
					return
				}
			}
		}()
		go func() {
			b := make([]byte, 300000)
			for {
				n, err := server.Read(b)
				if err != nil {
					if !raknet.ErrConnectionClosed(err) {
						log.Printf("error reading from server connection: %v\n", err)
					}
					_ = conn.Close()
					return
				}
				packet := b[:n]
				if _, err := conn.Write(packet); err != nil {
					if !raknet.ErrConnectionClosed(err) {
						log.Printf("error writing to client connection: %v\n", err)
					}
					_ = server.Close()
					return
				}
			}
		}()
	}
}
