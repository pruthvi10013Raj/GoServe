package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

const (
	maxMessageSize = 1024
	clientTimeout  = 5 * time.Second
)

func request(address, message string) (string, error) {
	if strings.ContainsAny(message, "\r\n") {
		return "", errors.New("message must be a single line")
	}
	if len(message) > maxMessageSize {
		return "", fmt.Errorf("message exceeds %d bytes", maxMessageSize)
	}

	conn, err := net.DialTimeout("tcp", address, clientTimeout)
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", address, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(clientTimeout)); err != nil {
		return "", fmt.Errorf("set connection deadline: %w", err)
	}
	if _, err := io.WriteString(conn, message+"\n"); err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}

	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return response, nil
}

func main() {
	address := flag.String("addr", "localhost:9090", "GoServe TCP server address")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/tcp-client [-addr host:port] <message>")
		os.Exit(2)
	}

	response, err := request(*address, flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "TCP client:", err)
		os.Exit(1)
	}
	fmt.Print(response)
}
