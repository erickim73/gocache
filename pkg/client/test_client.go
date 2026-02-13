package client

import (
	"net"
	// "fmt"
	"log/slog"
)

func TestClient() {  
	conn, err := net.Dial("tcp", "localhost:6379")
	if err != nil {
		slog.Error("Error connecting to server", "address", "localhost:6379", "error", err)
		return
	}
	defer conn.Close()

	// SET key: testing
	resp := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$7\r\ntesting\r\n"
	conn.Write([]byte(resp))

	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	slog.Info("Server response for SET", "response", string(buf[:n]))

	// GET key
	resp2 := "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"
	conn.Write([]byte(resp2))

	buf2 := make([]byte, 1024)
	n, _ = conn.Read(buf2)
	slog.Info("Value for key from GET", "value", string(buf2[:n]))

	// DEL key
	resp3 := "*2\r\n$3\r\nDEL\r\n$3\r\nkey\r\n"
	conn.Write([]byte(resp3))

	buf3 := make([]byte, 1024)
	n, _ = conn.Read(buf3)
	slog.Info("Deleted response for DEL", "response", string(buf[:n]))

	// GET key
	resp4 := "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"
	conn.Write([]byte(resp4))

	buf4 := make([]byte, 1024)
	n, _ = conn.Read(buf4)
	slog.Info("Value for key after deletion", "value", string(buf4[:n]))

	// FDS key
	resp5 := "*2\r\n$3\r\nFDS\r\n$3\r\nkey\r\n"
	conn.Write([]byte(resp5))

	buf5 := make([]byte, 1024)
	n, _ = conn.Read(buf5)
	slog.Info("FDS command response", "response", string(buf5[:n]))
}

