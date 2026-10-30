package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	maxTCPRequestSize    = 1024
	tcpConnectionTimeout = 5 * time.Second
	readHeaderTimeout    = 5 * time.Second
	idleTimeout          = 60 * time.Second
)

func basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		username, password, ok := r.BasicAuth()

		if !ok || username != "admin" || password != "goserve123" {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoServe"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		start := time.Now()

		next.ServeHTTP(w, r)

		duration := time.Since(start)

		log.Printf(
			"%s %s %v",
			r.Method,
			r.URL.Path,
			duration,
		)
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

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	config := defaultServerConfig()
	uploadHandlerWithConfig(w, r, config)
}

func uploadHandlerWithConfig(w http.ResponseWriter, r *http.Request, config serverConfig) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, config.MaxUploadSize)

	err := r.ParseMultipartForm(config.MaxUploadSize)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	if err != nil {
		http.Error(w, "File too large or invalid upload", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")

	if err != nil {
		http.Error(w, "File is required", http.StatusBadRequest)
		return
	}

	defer file.Close()

	filename := filepath.Base(header.Filename)

	if err := os.MkdirAll(config.UploadDirectory, 0755); err != nil {
		log.Printf("Unable to create upload directory: %v", err)
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	outputFile, outputPath, err := createUniqueUploadFile(config.UploadDirectory, filename)
	if err != nil {
		log.Printf("Unable to create upload file: %v", err)
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	_, err = io.Copy(outputFile, file)
	closeErr := outputFile.Close()

	if err != nil {
		if removeErr := os.Remove(outputPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			log.Printf("Unable to remove incomplete upload: %v", removeErr)
		}
		log.Printf("Unable to write upload file: %v", err)
		http.Error(w, "Unable to write file", http.StatusInternalServerError)
		return
	}

	if closeErr != nil {
		if removeErr := os.Remove(outputPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			log.Printf("Unable to remove incomplete upload: %v", removeErr)
		}
		log.Printf("Unable to close upload file: %v", closeErr)
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "File uploaded successfully: %s\n", filepath.Base(outputPath))
}

func newHTTPHandler(directory string) http.Handler {
	config := defaultServerConfig()
	config.StaticDirectory = directory
	return newConfiguredHTTPHandler(config)
}

func newConfiguredHTTPHandler(config serverConfig) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", loggingMiddleware(http.FileServer(http.Dir(config.StaticDirectory))))
	mux.Handle("/upload", loggingMiddleware(basicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadHandlerWithConfig(w, r, config)
	}))))
	return mux
}

func newHTTPServer(config serverConfig) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", config.HTTPSPort),
		Handler:           newConfiguredHTTPHandler(config),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func handleTCPConnection(conn net.Conn) {
	handleTCPConnectionWithTimeout(conn, tcpConnectionTimeout)
}

func handleTCPConnectionWithTimeout(conn net.Conn, timeout time.Duration) {
	defer conn.Close()

	log.Println("TCP client connected:", conn.RemoteAddr())

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		log.Println("TCP read deadline error:", err)
		return
	}

	reader := bufio.NewReaderSize(conn, maxTCPRequestSize+1)
	request, err := reader.ReadSlice('\n')

	if err == bufio.ErrBufferFull || len(request) > maxTCPRequestSize+1 {
		if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			log.Println("TCP write deadline error:", err)
			return
		}
		if _, err := io.WriteString(conn, "ERR request too long\n"); err != nil {
			log.Println("TCP write error:", err)
		}
		return
	}

	if err != nil {
		if err != io.EOF || len(request) > 0 {
			log.Println("TCP read error:", err)
		}
		return
	}

	request = request[:len(request)-1]
	if len(request) > 0 && request[len(request)-1] == '\r' {
		request = request[:len(request)-1]
	}

	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		log.Println("TCP write deadline error:", err)
		return
	}

	if _, err := fmt.Fprintf(conn, "Received: %s\n", request); err != nil {
		log.Println("TCP write error:", err)
	}
}

func startTCPServer(port string) {

	listener, err := net.Listen("tcp", port)

	if err != nil {
		log.Println("TCP server error:", err)
		return
	}

	defer listener.Close()

	log.Println("GoServe TCP server running on", port)

	for {
		conn, err := listener.Accept()

		if err != nil {
			log.Println("TCP connection error:", err)
			continue
		}

		go handleTCPConnection(conn)
	}
}

func run(args []string) error {
	config, err := parseServerConfig(args)
	if err != nil {
		return err
	}

	go startTCPServer(fmt.Sprintf(":%d", config.TCPPort))

	server := newHTTPServer(config)
	fmt.Printf("GoServe running on https://localhost%s\n", server.Addr)
	fmt.Printf("Serving directory: %s\n", config.StaticDirectory)

	if err := server.ListenAndServeTLS(
		"cert/server.crt",
		"cert/server.key",
	); err != nil {
		return fmt.Errorf("HTTPS server stopped: %w", err)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
