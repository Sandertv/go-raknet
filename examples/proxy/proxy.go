package main

import (
	"github.com/sandertv/raknet"
	"log"
)

func main() {
	listener, err := raknet.Listen("0.0.0.0:19132")
	defer func() {
		_ = listener.Close()
	}()
	if err != nil {
		panic(err)
	}
	// We hijack the pong of a Minecraft server, so our proxy will continuously send the pong data of the
	// server.
	if err := listener.HijackPong("mco.mineplex.com:19132"); err != nil {
		panic(err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		// We spin up a new connection with the server each time a client connects to the proxy.
		server, err := raknet.Dial("mco.mineplex.com:19132")
		if err != nil {
			panic(err)
		}
		go func() {
			b := make([]byte, conn.MaxPacketSize())
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
			b := make([]byte, conn.MaxPacketSize())
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
