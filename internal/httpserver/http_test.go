package httpserver

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"goserve/internal/config"
)

func TestUploadHandlerCreatesDirectoryAndUniqueNames(t *testing.T) {
	uploadDirectory := filepath.Join(t.TempDir(), "uploads")
	config := testServerConfig()
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
	config := testServerConfig()
	config.StaticDirectory = t.TempDir()
	config.UploadDirectory = filepath.Join(t.TempDir(), "uploads")
	handler := newConfiguredHTTPHandler(config)

	invalidRequest := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("not multipart"))
	invalidRequest.Header.Set("Content-Type", "text/plain")
	invalidRequest.SetBasicAuth(testUploadUsername, testUploadPassword)
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
	missingRequest.SetBasicAuth(testUploadUsername, testUploadPassword)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusBadRequest {
		t.Errorf("missing file status = %d, want %d", missingResponse.Code, http.StatusBadRequest)
	}
}

func TestUploadHandlerSanitizesClientFilename(t *testing.T) {
	uploadDirectory := filepath.Join(t.TempDir(), "uploads")
	config := testServerConfig()
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
	config := testServerConfig()
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

	config := testServerConfig()
	config.StaticDirectory = t.TempDir()
	handler := newConfiguredHTTPHandler(config)
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
	config := testServerConfig()
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
	config := testServerConfig()
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

func TestUploadCredentialsRequireBothEnvironmentValues(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		wantErr  bool
	}{
		{name: "both configured", username: "local-user", password: "local-password"},
		{name: "missing username", password: "local-password", wantErr: true},
		{name: "missing password", username: "local-user", wantErr: true},
		{name: "blank credentials", username: " ", password: "\t", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{
				usernameEnv: test.username,
				passwordEnv: test.password,
			}
			credentials, err := CredentialsFromEnvironment(func(key string) string {
				return values[key]
			})
			if test.wantErr {
				if err == nil {
					t.Fatal("loading missing credentials succeeded, want error")
				}
				if (strings.TrimSpace(test.username) != "" && strings.Contains(err.Error(), test.username)) ||
					(strings.TrimSpace(test.password) != "" && strings.Contains(err.Error(), test.password)) {
					t.Errorf("credential error exposes a secret value: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("load configured credentials: %v", err)
			}
			if credentials.Username != test.username || credentials.Password != test.password {
				t.Fatal("loaded credentials do not match supplied values")
			}
		})
	}
}

func TestUploadAuthenticationFailsClosedWhenNotConfigured(t *testing.T) {
	config := defaultServerConfig()
	config.StaticDirectory = t.TempDir()
	handler := newConfiguredHTTPHandler(config)
	request := httptest.NewRequest(http.MethodPost, "/upload", nil)
	request.SetBasicAuth("", "")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured upload auth status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestUploadRoutePreservesAuthenticationAndMethodStatuses(t *testing.T) {
	config := testServerConfig()
	config.StaticDirectory = t.TempDir()
	config.UploadDirectory = filepath.Join(t.TempDir(), "uploads")
	handler := newConfiguredHTTPHandler(config)

	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodPost, "/upload", nil)
	unauthorizedRequest.SetBasicAuth(testUploadUsername, "wrong-password")
	handler.ServeHTTP(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Errorf("wrong credentials status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	authorized := httptest.NewRecorder()
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/upload", nil)
	authorizedRequest.SetBasicAuth(testUploadUsername, testUploadPassword)
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusMethodNotAllowed {
		t.Errorf("authenticated GET /upload status = %d, want %d", authorized.Code, http.StatusMethodNotAllowed)
	}

	descendant := httptest.NewRecorder()
	descendantRequest := httptest.NewRequest(http.MethodPost, "/upload/private", nil)
	handler.ServeHTTP(descendant, descendantRequest)
	if descendant.Code != http.StatusNotFound {
		t.Errorf("POST /upload/private status = %d, want %d", descendant.Code, http.StatusNotFound)
	}
}

func TestHTTPLoggingDoesNotIncludeCredentialsAuthorizationOrUploadBody(t *testing.T) {
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(previousLogOutput)
	})

	config := testServerConfig()
	config.StaticDirectory = t.TempDir()
	config.UploadDirectory = filepath.Join(t.TempDir(), "uploads")
	handler := newConfiguredHTTPHandler(config)

	rootResponse := httptest.NewRecorder()
	handler.ServeHTTP(rootResponse, httptest.NewRequest(http.MethodGet, "/", nil))

	const privatePayload = "private-upload-payload"
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, newUploadRequest(t, "private.txt", privatePayload))
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want %d", uploadResponse.Code, http.StatusOK)
	}

	logOutput := logs.String()
	for _, expected := range []string{"GET /", "POST /upload"} {
		if !strings.Contains(logOutput, expected) {
			t.Errorf("logs %q do not include route entry %q", logOutput, expected)
		}
	}
	for _, secret := range []string{
		testUploadUsername,
		testUploadPassword,
		"Authorization",
		"Basic ",
		privatePayload,
	} {
		if strings.Contains(logOutput, secret) {
			t.Errorf("logs expose sensitive value %q: %s", secret, logOutput)
		}
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
	request.SetBasicAuth(testUploadUsername, testUploadPassword)
	return request
}

const (
	testUploadUsername = "test-upload-user"
	testUploadPassword = "test-upload-password"
)

func testServerConfig() serverConfig {
	config := defaultServerConfig()
	config.UploadAuth = Credentials{
		Username: testUploadUsername,
		Password: testUploadPassword,
	}
	return config
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

type serverConfig struct {
	config.Config
	UploadAuth Credentials
}

func defaultServerConfig() serverConfig {
	return serverConfig{Config: config.Default()}
}

func newConfiguredHTTPHandler(serverConfig serverConfig) http.Handler {
	return NewHandler(ServerConfig{Config: serverConfig.Config, UploadAuth: serverConfig.UploadAuth})
}

func newHTTPServer(serverConfig serverConfig) *http.Server {
	return NewServer(ServerConfig{Config: serverConfig.Config, UploadAuth: serverConfig.UploadAuth})
}
