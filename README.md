# go-raknet

go-raknet is a library that implements a basic version of the RakNet protocol, which is used for
Minecraft (Bedrock Edition). It implements Unreliable, Reliable and 
ReliableOrdered packets and sends user packets as ReliableOrdered.

go-raknet attempts to abstract away direct interaction with RakNet, and provides simple to use, idiomatic Go
API used to listen for connections or connect to servers.

## Getting started

### Prerequisites
To use this library, at least **Go 1.18** must be installed.

### Usage
go-raknet can be used for both clients and servers, (and proxies, when combined) in a way very similar to the
standard net.TCP* functions.

Basic RakNet server:
```go
package main

import (
	"github.com/sandertv/go-raknet"
)

func main() {
    listener, _ := raknet.Listen("0.0.0.0:19132")
    defer listener.Close()
    for {
        conn, _ := listener.Accept()
        
        b := make([]byte, 1024*1024*4)
        _, _ = conn.Read(b)
        _, _ = conn.Write([]byte{1, 2, 3})
        
        conn.Close()
    }
}
```

Basic RakNet client:

```go
package main

import (
	"github.com/sandertv/go-raknet"
)

func main() {
    conn, _ := raknet.Dial("mco.mineplex.com:19132")
    defer conn.Close()
    
    b := make([]byte, 1024*1024*4)
    _, _ = conn.Write([]byte{1, 2, 3})
    _, _ = conn.Read(b)
}
```

### Documentation
[![PkgGoDev](https://pkg.go.dev/badge/github.com/sandertv/go-raknet)](https://pkg.go.dev/github.com/sandertv/go-raknet)

## Contact
[![Discord Banner 2](https://discordapp.com/api/guilds/623638955262345216/widget.png?style=banner2)](https://discord.gg/U4kFWHhTNR)
