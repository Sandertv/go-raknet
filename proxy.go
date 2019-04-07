package raknet

import (
	"bufio"
	"compress/zlib"
	"fmt"
)

// Proxy wraps around a client and a server connection and provides basic proxy functionality.
type Proxy struct {
	client *Conn
	server *Conn
}

// NewProxy returns a proxy wrapping around the client connection passed. proxy.Direct() must be called to
// connect the proxy to a server.
func NewProxy(clientConn *Conn) *Proxy {
	return &Proxy{client: clientConn}
}

// Direct directs the traffic of the client connection to the address passed. Before connecting to the
// address, Direct first closes a previous connection if one was active. It then dials a new one.
// If dialing a new connection was no successful, an error is returned.
func (proxy *Proxy) Direct(address string) error {
	if proxy.server != nil {
		if err := proxy.server.Close(); err != nil {
			return fmt.Errorf("error closing previous server connection: %v" ,err)
		}
	}
	serverConn, err := Dial(address)
	if err != nil {
		return fmt.Errorf("error dialing server connection: %v", err)
	}
	proxy.server = serverConn
	return nil
}

func (proxy *Proxy) ReadFromClient(b []byte) (n int, err error) {
	reader, err := zlib.NewReader(bufio.NewReaderSize(proxy.client, len(b)))
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = reader.Close()
	}()
	return reader.Read(b)
}

func (proxy *Proxy) ReadFromServer(b []byte) (n int, err error) {
	reader, err := zlib.NewReader(bufio.NewReaderSize(proxy.server, len(b)))
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = reader.Close()
	}()
	return reader.Read(b)
}

func (proxy *Proxy) WriteToClient(b []byte) (n int, err error) {
	w := bufio.NewWriterSize(proxy.client, len(b))
	writer := zlib.NewWriter(w)
	defer func() {
		_ = writer.Close()
		_ = w.Flush()
	}()
	return writer.Write(b)
}

func (proxy *Proxy) WriteToServer(b []byte) (n int, err error) {
	w := bufio.NewWriterSize(proxy.server, len(b))
	writer := zlib.NewWriter(w)
	defer func() {
		_ = writer.Close()
		_ = w.Flush()
	}()
	return writer.Write(b)
}

// Close closes both connections of the proxy; The client's and the server's. If an error occurs in either,
// it is returned.
func (proxy *Proxy) Close() error {
	if err := proxy.client.Close(); err != nil {
		return fmt.Errorf("error closing client connection: %v", err)
	}
	if err := proxy.server.Close(); err != nil {
		return fmt.Errorf("error closing server connection: %v", err)
	}
	return nil
}