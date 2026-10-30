package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandleTCPConnectionRespondsWithReceivedMessage(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		handleTCPConnection(serverConn)
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

	go handleTCPConnection(serverConn)

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

	go handleTCPConnection(serverConn)
	if _, err := clientConn.Write([]byte(strings.Repeat("x", maxTCPRequestSize+1))); err != nil {
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

	go handleTCPConnection(serverConn)
	message := strings.Repeat("x", maxTCPRequestSize)
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

func TestHandleTCPConnectionReturnsAfterClientDisconnect(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		handleTCPConnection(serverConn)
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
		handleTCPConnectionWithTimeout(serverConn, 50*time.Millisecond)
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

func TestUploadHandlerCreatesDirectoryAndUniqueNames(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	handler := newHTTPHandler(t.TempDir())
	first := postUpload(t, handler, "test.txt", "first upload")
	second := postUpload(t, handler, "test.txt", "second upload")

	if first.Code != http.StatusOK {
		t.Fatalf("first upload status = %d, want %d", first.Code, http.StatusOK)
	}
	if second.Code != http.StatusOK {
		t.Fatalf("second upload status = %d, want %d", second.Code, http.StatusOK)
	}
	if want := "File uploaded successfully: test.txt\n"; first.Body.String() != want {
		t.Errorf("first upload response = %q, want %q", first.Body.String(), want)
	}
	if want := "File uploaded successfully: test_1.txt\n"; second.Body.String() != want {
		t.Errorf("second upload response = %q, want %q", second.Body.String(), want)
	}

	for filename, want := range map[string]string{
		"test.txt":   "first upload",
		"test_1.txt": "second upload",
	} {
		got, err := os.ReadFile(filepath.Join("uploads", filename))
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		if string(got) != want {
			t.Errorf("%s contents = %q, want %q", filename, got, want)
		}
	}
}

func TestCreateUniqueUploadFileIsSafeForConcurrentCalls(t *testing.T) {
	const uploadCount = 16
	directory := t.TempDir()
	paths := make(chan string, uploadCount)
	errs := make(chan error, uploadCount)

	var workers sync.WaitGroup
	workers.Add(uploadCount)
	for i := 0; i < uploadCount; i++ {
		go func(index int) {
			defer workers.Done()

			file, path, err := createUniqueUploadFile(directory, "shared.txt")
			if err != nil {
				errs <- err
				return
			}
			if _, err := fmt.Fprintf(file, "upload %d", index); err != nil {
				errs <- err
				_ = file.Close()
				return
			}
			if err := file.Close(); err != nil {
				errs <- err
				return
			}
			paths <- path
		}(i)
	}
	workers.Wait()
	close(paths)
	close(errs)

	for err := range errs {
		t.Errorf("create concurrent upload: %v", err)
	}

	seen := make(map[string]bool)
	for path := range paths {
		if seen[path] {
			t.Errorf("duplicate upload path allocated: %s", path)
		}
		seen[path] = true
	}
	if len(seen) != uploadCount {
		t.Errorf("allocated %d unique paths, want %d", len(seen), uploadCount)
	}
}

func TestUploadRouteLogsUnauthorizedRequests(t *testing.T) {
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(previousLogOutput)
	})

	handler := newHTTPHandler(t.TempDir())
	request := httptest.NewRequest(http.MethodGet, "/upload", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("upload status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(logs.String(), "GET /upload") {
		t.Errorf("request log %q does not include upload route", logs.String())
	}
}

func TestConfiguredHTTPHandlerUsesUploadDirectoryAndSizeLimit(t *testing.T) {
	root := t.TempDir()
	staticDirectory := filepath.Join(root, "public")
	if err := os.MkdirAll(staticDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDirectory, "hello.txt"), []byte("static content"), 0600); err != nil {
		t.Fatal(err)
	}
	uploadDirectory := filepath.Join(root, "stored")
	config := defaultServerConfig()
	config.StaticDirectory = staticDirectory
	config.UploadDirectory = uploadDirectory
	config.MaxUploadSize = 512
	handler := newConfiguredHTTPHandler(config)

	staticResponse := httptest.NewRecorder()
	handler.ServeHTTP(staticResponse, httptest.NewRequest(http.MethodGet, "/hello.txt", nil))
	if staticResponse.Code != http.StatusOK || staticResponse.Body.String() != "static content" {
		t.Fatalf("static response = (%d, %q), want (200, %q)", staticResponse.Code, staticResponse.Body.String(), "static content")
	}

	uploadResponse := postUpload(t, handler, "saved.txt", "small")
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want %d", uploadResponse.Code, http.StatusOK)
	}
	saved, err := os.ReadFile(filepath.Join(uploadDirectory, "saved.txt"))
	if err != nil {
		t.Fatalf("read configured upload: %v", err)
	}
	if string(saved) != "small" {
		t.Errorf("saved content = %q, want %q", saved, "small")
	}

	tooLarge := postUpload(t, handler, "large.txt", strings.Repeat("x", 1024))
	if tooLarge.Code != http.StatusBadRequest {
		t.Errorf("oversized upload status = %d, want %d", tooLarge.Code, http.StatusBadRequest)
	}
	if _, err := os.Stat(filepath.Join(uploadDirectory, "large.txt")); !os.IsNotExist(err) {
		t.Errorf("oversized upload file exists or stat failed unexpectedly: %v", err)
	}
}

func TestNewHTTPServerConfiguresAddressHandlerAndTimeouts(t *testing.T) {
	config := defaultServerConfig()
	config.HTTPSPort = 9443
	config.StaticDirectory = t.TempDir()

	server := newHTTPServer(config)
	if server.Addr != ":9443" {
		t.Errorf("server address = %q, want %q", server.Addr, ":9443")
	}
	if server.Handler == nil {
		t.Fatal("server handler is nil")
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("read-header timeout = %v, want %v", server.ReadHeaderTimeout, 5*time.Second)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Errorf("idle timeout = %v, want %v", server.IdleTimeout, 60*time.Second)
	}

	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hello.txt", nil))
	if response.Code != http.StatusNotFound {
		t.Errorf("static route status = %d, want %d", response.Code, http.StatusNotFound)
	}

	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/upload", nil))
	if response.Code != http.StatusUnauthorized {
		t.Errorf("upload route status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func postUpload(t *testing.T, handler http.Handler, filename, content string) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.SetBasicAuth("admin", "goserve123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
