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
	maxMessageSize  = 1024
	maxResponseSize = len("Received: ") + maxMessageSize + 1
	clientTimeout   = 5 * time.Second
)

func request(address, message string) (string, error) {
	return requestWithTimeout(address, message, clientTimeout)
}

func requestWithTimeout(address, message string, timeout time.Duration) (string, error) {
	if strings.ContainsAny(message, "\r\n") {
		return "", errors.New("message must be a single line")
	}
	if len(message) > maxMessageSize {
		return "", fmt.Errorf("message exceeds %d bytes", maxMessageSize)
	}

	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", address, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", fmt.Errorf("set connection deadline: %w", err)
	}
	if _, err := io.WriteString(conn, message+"\n"); err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}

	response, err := bufio.NewReaderSize(conn, maxResponseSize+1).ReadSlice('\n')
	if err == bufio.ErrBufferFull || len(response) > maxResponseSize {
		return "", fmt.Errorf("response exceeds %d bytes", maxResponseSize)
	}
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return string(response), nil
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
