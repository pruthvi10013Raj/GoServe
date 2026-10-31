package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRunCleansTCPListenerWhenHTTPSStartupFails(t *testing.T) {
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpPort := reservation.Addr().(*net.TCPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}

	staticDirectory := t.TempDir()
	startupErr := errors.New("simulated TLS certificate startup failure")
	err = runWithHTTPS(context.Background(), []string{
		"-https-port", fmt.Sprint(tcpPort),
		"-tcp-port", fmt.Sprint(tcpPort),
		"-static-dir", staticDirectory,
	}, func(name string) string {
		if name == "GOSERVE_UPLOAD_USERNAME" {
			return "test-upload-user"
		}
		return "test-upload-password"
	}, func(*http.Server) error {
		return startupErr
	})
	if !errors.Is(err, startupErr) || !strings.Contains(err.Error(), "HTTPS server stopped") {
		t.Fatalf("run error = %v, want wrapped HTTPS startup error", err)
	}

	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(tcpPort)), 100*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatal("TCP listener still accepts connections after HTTPS startup failed")
	}
}

func TestShutdownHTTPServerWaitsForActiveRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(requestStarted)
			<-releaseRequest
			_, _ = io.WriteString(w, "finished")
		}),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()

	responseDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			defer response.Body.Close()
			_, err = io.Copy(io.Discard, response.Body)
		}
		responseDone <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		close(releaseRequest)
		t.Fatal("HTTP request did not reach handler")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownHTTPServer(server)
	}()

	select {
	case err := <-shutdownDone:
		close(releaseRequest)
		t.Fatalf("shutdown returned before active request ended: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRequest)
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("graceful HTTP shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP shutdown did not complete after active request was released")
	}
	if err := <-responseDone; err != nil {
		t.Errorf("active HTTP request: %v", err)
	}
	if err := <-serveDone; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("Serve error = %v, want %v", err, http.ErrServerClosed)
	}
}
