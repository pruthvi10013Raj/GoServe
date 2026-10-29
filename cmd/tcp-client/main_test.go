package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestRequestSendsMessageAndReadsResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			serverErr <- err
			return
		}
		message, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			serverErr <- err
			return
		}
		if message != "hello GoServe\n" {
			serverErr <- fmt.Errorf("request = %q, want %q", message, "hello GoServe\n")
			return
		}
		if _, err := fmt.Fprint(conn, "Received: hello GoServe\n"); err != nil {
			serverErr <- err
			return
		}
		_, err = bufio.NewReader(conn).ReadByte()
		if err != io.EOF {
			serverErr <- fmt.Errorf("client close error = %v, want EOF", err)
			return
		}
		serverErr <- nil
	}()

	response, err := request(listener.Addr().String(), "hello GoServe")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if want := "Received: hello GoServe\n"; response != want {
		t.Errorf("response = %q, want %q", response, want)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test server: %v", err)
	}
}

func TestRequestRejectsInvalidMessagesBeforeConnecting(t *testing.T) {
	for _, message := range []string{"two\nlines", string(make([]byte, maxMessageSize+1))} {
		if _, err := request("invalid address", message); err == nil {
			t.Errorf("request(%q) succeeded, want validation error", message)
		}
	}
}

func TestRequestRejectsOversizedResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(conn, string(make([]byte, maxResponseSize+1))+"\n")
	}()

	if _, err := request(listener.Addr().String(), "hello"); err == nil {
		t.Fatal("request succeeded, want oversized response error")
	}
}

func TestRequestReturnsErrorOnServerDisconnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	if _, err := request(listener.Addr().String(), "hello"); err == nil {
		t.Fatal("request succeeded, want server disconnect error")
	}
}

func TestRequestDeadlineBoundsResponseWait(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadString('\n')
		_, _ = bufio.NewReader(conn).ReadByte()
	}()

	if _, err := requestWithTimeout(listener.Addr().String(), "hello", 50*time.Millisecond); err == nil {
		t.Fatal("request succeeded, want response timeout")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("test server did not observe client close after timeout")
	}
}
