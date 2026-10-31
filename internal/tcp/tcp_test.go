package tcp

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestHandleTCPConnectionRespondsWithReceivedMessage(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		HandleConnection(serverConn)
	}()

	if _, err := clientConn.Write([]byte("hello GoServe\r\n")); err != nil {
		t.Fatalf("write request: %v", err)
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	response, err := bufio.NewReader(clientConn).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if want := "Received: hello GoServe\n"; response != want {
		t.Errorf("response = %q, want %q", response, want)
	}
	if _, err := bufio.NewReader(clientConn).ReadByte(); err != io.EOF {
		t.Errorf("read after response error = %v, want EOF", err)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server handler did not return after responding")
	}
}

func TestHandleTCPConnectionAcceptsEmptyRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go HandleConnection(serverConn)

	if _, err := clientConn.Write([]byte("\n")); err != nil {
		t.Fatalf("write empty request: %v", err)
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	response, err := bufio.NewReader(clientConn).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if want := "Received: \n"; response != want {
		t.Errorf("response = %q, want %q", response, want)
	}
}

func TestHandleTCPConnectionRejectsOversizedRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go HandleConnection(serverConn)
	if _, err := clientConn.Write([]byte(strings.Repeat("x", MaxRequestSize+1) + "\n")); err != nil {
		t.Fatalf("write oversized request: %v", err)
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	response, err := bufio.NewReader(clientConn).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if want := "ERR request too long\n"; response != want {
		t.Errorf("response = %q, want %q", response, want)
	}
}

func TestHandleTCPConnectionAcceptsMaximumRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go HandleConnection(serverConn)
	message := strings.Repeat("x", MaxRequestSize)
	if _, err := clientConn.Write([]byte(message + "\n")); err != nil {
		t.Fatalf("write maximum request: %v", err)
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	response, err := bufio.NewReader(clientConn).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if want := "Received: " + message + "\n"; response != want {
		t.Errorf("response length = %d, want %d", len(response), len(want))
	}
}

func TestHandleTCPConnectionAcceptsMaximumCRLFRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go HandleConnection(serverConn)
	message := strings.Repeat("x", MaxRequestSize)
	if _, err := clientConn.Write([]byte(message + "\r\n")); err != nil {
		t.Fatalf("write maximum CRLF request: %v", err)
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	response, err := bufio.NewReader(clientConn).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if want := "Received: " + message + "\n"; response != want {
		t.Errorf("response length = %d, want %d", len(response), len(want))
	}
}

func TestHandleTCPConnectionRejectsOversizedCRLFRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go HandleConnection(serverConn)
	message := strings.Repeat("x", MaxRequestSize+1)
	go func() {
		_, _ = clientConn.Write([]byte(message + "\r\n"))
	}()
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	response, err := bufio.NewReader(clientConn).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if want := "ERR request too long\n"; response != want {
		t.Errorf("response = %q, want %q", response, want)
	}
}

func TestHandleTCPConnectionReturnsAfterClientDisconnect(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		HandleConnection(serverConn)
	}()

	if _, err := clientConn.Write([]byte("partial")); err != nil {
		t.Fatalf("write partial request: %v", err)
	}
	if err := clientConn.Close(); err != nil {
		t.Fatalf("close client connection: %v", err)
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server handler did not return after client disconnect")
	}
}

func TestHandleTCPConnectionClosesIdleClientAfterDeadline(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		handleConnectionWithTimeout(serverConn, 50*time.Millisecond)
	}()

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, err := bufio.NewReader(clientConn).ReadByte(); err != io.EOF {
		t.Fatalf("read from idle connection error = %v, want EOF", err)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server handler did not return after read deadline")
	}
}

func TestTCPServerShutdownClosesListenerAndActiveClients(t *testing.T) {
	server, err := StartServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start TCP server: %v", err)
	}

	client, err := net.Dial("tcp", server.listener.Addr().String())
	if err != nil {
		t.Fatalf("connect TCP client: %v", err)
	}
	defer client.Close()

	if _, err := io.WriteString(client, "unfinished"); err != nil {
		t.Fatalf("write partial request: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		server.mu.Lock()
		activeConnections := len(server.conns)
		server.mu.Unlock()
		if activeConnections > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TCP server did not register the client connection")
		}
		time.Sleep(time.Millisecond)
	}

	if err := server.Shutdown(); err != nil {
		t.Fatalf("shutdown TCP server: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Error("client read after shutdown succeeded, want closed connection")
	}
	if _, err := net.DialTimeout("tcp", server.listener.Addr().String(), 100*time.Millisecond); err == nil {
		t.Error("new connection after shutdown succeeded, want refused connection")
	}
}
