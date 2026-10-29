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

	go handleTCPConnection(serverConn)

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
