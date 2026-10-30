package main

import (
	"bufio"
	"bytes"
	"errors"
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
	uploadDirectory := filepath.Join(t.TempDir(), "uploads")
	config := defaultServerConfig()
	config.StaticDirectory = t.TempDir()
	config.UploadDirectory = uploadDirectory
	handler := newConfiguredHTTPHandler(config)
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
		got, err := os.ReadFile(filepath.Join(uploadDirectory, filename))
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		if string(got) != want {
			t.Errorf("%s contents = %q, want %q", filename, got, want)
		}
	}
}

func TestUploadHandlerRejectsInvalidAndMissingFileFields(t *testing.T) {
	config := defaultServerConfig()
	config.StaticDirectory = t.TempDir()
	config.UploadDirectory = filepath.Join(t.TempDir(), "uploads")
	handler := newConfiguredHTTPHandler(config)

	invalidRequest := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("not multipart"))
	invalidRequest.Header.Set("Content-Type", "text/plain")
	invalidRequest.SetBasicAuth("admin", "goserve123")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Errorf("invalid multipart status = %d, want %d", invalidResponse.Code, http.StatusBadRequest)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("other", "value"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	missingRequest := httptest.NewRequest(http.MethodPost, "/upload", &body)
	missingRequest.Header.Set("Content-Type", writer.FormDataContentType())
	missingRequest.SetBasicAuth("admin", "goserve123")
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusBadRequest {
		t.Errorf("missing file status = %d, want %d", missingResponse.Code, http.StatusBadRequest)
	}
}

func TestUploadHandlerSanitizesClientFilename(t *testing.T) {
	uploadDirectory := filepath.Join(t.TempDir(), "uploads")
	config := defaultServerConfig()
	config.StaticDirectory = t.TempDir()
	config.UploadDirectory = uploadDirectory
	handler := newConfiguredHTTPHandler(config)

	response := postUpload(t, handler, `..\..\escape.txt`, "safe contents")
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want %d", response.Code, http.StatusOK)
	}
	if want := "File uploaded successfully: escape.txt\n"; response.Body.String() != want {
		t.Fatalf("response = %q, want %q", response.Body.String(), want)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(uploadDirectory), "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("file was created outside upload directory; stat error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(uploadDirectory, "escape.txt"))
	if err != nil {
		t.Fatalf("read sanitized upload: %v", err)
	}
	if string(got) != "safe contents" {
		t.Errorf("uploaded content = %q, want %q", got, "safe contents")
	}
}

func TestSaveUploadedFileRemovesPartialFileOnReadError(t *testing.T) {
	directory := t.TempDir()
	_, err := saveUploadedFile(directory, "partial.txt", &errorAfterBytesReader{})
	if err == nil {
		t.Fatal("save with failing source succeeded, want an error")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read upload directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("directory contains partial uploads: %v", entries)
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

func TestUploadHandlerAllocatesUniqueNamesForConcurrentDuplicates(t *testing.T) {
	const uploadCount = 16
	uploadDirectory := filepath.Join(t.TempDir(), "uploads")
	config := defaultServerConfig()
	config.StaticDirectory = t.TempDir()
	config.UploadDirectory = uploadDirectory
	handler := newConfiguredHTTPHandler(config)

	requests := make([]*http.Request, uploadCount)
	responses := make([]*httptest.ResponseRecorder, uploadCount)
	for i := 0; i < uploadCount; i++ {
		requests[i] = newUploadRequest(t, "shared.txt", fmt.Sprintf("upload %d", i))
		responses[i] = httptest.NewRecorder()
	}

	var workers sync.WaitGroup
	for i := range requests {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			handler.ServeHTTP(responses[index], requests[index])
		}(i)
	}
	workers.Wait()

	seen := make(map[string]bool, uploadCount)
	for i, response := range responses {
		if response.Code != http.StatusOK {
			t.Errorf("upload %d status = %d, want %d: %s", i, response.Code, http.StatusOK, response.Body.String())
			continue
		}
		filename := strings.TrimPrefix(response.Body.String(), "File uploaded successfully: ")
		filename = strings.TrimSpace(filename)
		if seen[filename] {
			t.Errorf("duplicate upload filename allocated: %s", filename)
			continue
		}
		seen[filename] = true
		got, err := os.ReadFile(filepath.Join(uploadDirectory, filename))
		if err != nil {
			t.Errorf("read concurrent upload %s: %v", filename, err)
			continue
		}
		if want := fmt.Sprintf("upload %d", i); string(got) != want {
			t.Errorf("%s contents = %q, want %q", filename, got, want)
		}
	}
	if len(seen) != uploadCount {
		t.Errorf("allocated %d unique filenames, want %d", len(seen), uploadCount)
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
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload status = %d, want %d", tooLarge.Code, http.StatusRequestEntityTooLarge)
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

func TestTCPServerShutdownClosesListenerAndActiveClients(t *testing.T) {
	server, err := startTCPServer("127.0.0.1:0")
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

	if err := server.shutdown(); err != nil {
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

func postUpload(t *testing.T, handler http.Handler, filename, content string) *httptest.ResponseRecorder {
	t.Helper()

	request := newUploadRequest(t, filename, content)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newUploadRequest(t *testing.T, filename, content string) *http.Request {
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
	return request
}

type errorAfterBytesReader struct {
	read bool
}

func (reader *errorAfterBytesReader) Read(buffer []byte) (int, error) {
	if reader.read {
		return 0, errors.New("simulated source read failure")
	}
	reader.read = true
	return copy(buffer, "partial contents"), errors.New("simulated source read failure")
}
