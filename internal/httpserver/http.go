package httpserver

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goserve/internal/config"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 60 * time.Second
	usernameEnv       = "GOSERVE_UPLOAD_USERNAME"
	passwordEnv       = "GOSERVE_UPLOAD_PASSWORD"
)

type Credentials struct {
	Username string
	Password string
}

type ServerConfig struct {
	config.Config
	UploadAuth Credentials
}

func CredentialsFromEnvironment(getenv func(string) string) (Credentials, error) {
	credentials := Credentials{
		Username: getenv(usernameEnv),
		Password: getenv(passwordEnv),
	}
	if strings.TrimSpace(credentials.Username) == "" || strings.TrimSpace(credentials.Password) == "" {
		return Credentials{}, fmt.Errorf("%s and %s must both be set to enable protected uploads", usernameEnv, passwordEnv)
	}
	return credentials, nil
}

func basicAuth(credentials Credentials, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(credentials.Username) == "" || strings.TrimSpace(credentials.Password) == "" {
			http.Error(w, "Upload authentication is not configured", http.StatusServiceUnavailable)
			return
		}

		username, password, ok := r.BasicAuth()
		if !ok || username != credentials.Username || password != credentials.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoServe"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, route, time.Since(start))
	})
}

func createUniqueUploadFile(directory, filename string) (*os.File, string, error) {
	extension := filepath.Ext(filename)
	name := filename[:len(filename)-len(extension)]

	for counter := 0; ; counter++ {
		candidate := filename
		if counter > 0 {
			candidate = fmt.Sprintf("%s_%d%s", name, counter, extension)
		}

		outputPath := filepath.Join(directory, candidate)
		outputFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
		if err == nil {
			return outputFile, outputPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
}

func sanitizeUploadFilename(filename string) (string, error) {
	filename = strings.ReplaceAll(filename, `\`, "/")
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == ".." ||
		strings.ContainsAny(filename, `/\`) || strings.ContainsRune(filename, '\x00') {
		return "", fmt.Errorf("invalid upload filename")
	}
	return filename, nil
}

func saveUploadedFile(directory, filename string, source io.Reader) (string, error) {
	filename, err := sanitizeUploadFilename(filename)
	if err != nil {
		return "", err
	}

	outputFile, outputPath, err := createUniqueUploadFile(directory, filename)
	if err != nil {
		return "", fmt.Errorf("create upload file: %w", err)
	}

	_, copyErr := io.Copy(outputFile, source)
	closeErr := outputFile.Close()
	saveErr := error(nil)
	if copyErr != nil {
		saveErr = fmt.Errorf("write upload file: %w", copyErr)
	}
	if closeErr != nil {
		saveErr = errors.Join(saveErr, fmt.Errorf("close upload file: %w", closeErr))
	}
	if saveErr != nil {
		if removeErr := os.Remove(outputPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return "", errors.Join(saveErr, fmt.Errorf("remove incomplete upload: %w", removeErr))
		}
		return "", saveErr
	}

	return filepath.Base(outputPath), nil
}

func uploadHandlerWithConfig(w http.ResponseWriter, r *http.Request, serverConfig config.Config) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, serverConfig.MaxUploadSize)

	err := r.ParseMultipartForm(serverConfig.MaxUploadSize)
	if r.MultipartForm != nil {
		defer func() {
			if err := r.MultipartForm.RemoveAll(); err != nil {
				log.Printf("Unable to clean multipart temporary files: %v", err)
			}
		}()
	}

	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			http.Error(w, "Upload too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Invalid multipart upload", http.StatusBadRequest)
		}
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File is required", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("Unable to close uploaded request file: %v", err)
		}
	}()

	filename, err := sanitizeUploadFilename(header.Filename)
	if err != nil {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(serverConfig.UploadDirectory, 0755); err != nil {
		log.Printf("Unable to create upload directory: %v", err)
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	savedFilename, err := saveUploadedFile(serverConfig.UploadDirectory, filename, file)
	if err != nil {
		log.Printf("Unable to save upload file: %v", err)
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "File uploaded successfully: %s\n", savedFilename)
}

func NewHandler(serverConfig ServerConfig) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", loggingMiddleware("/", http.FileServer(http.Dir(serverConfig.StaticDirectory))))
	mux.Handle("/upload", loggingMiddleware("/upload", basicAuth(serverConfig.UploadAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadHandlerWithConfig(w, r, serverConfig.Config)
	}))))
	return mux
}

func NewServer(serverConfig ServerConfig) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", serverConfig.HTTPSPort),
		Handler:           NewHandler(serverConfig),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}
