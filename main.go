package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxTCPRequestSize    = 1024
	tcpConnectionTimeout = 5 * time.Second
	readHeaderTimeout    = 5 * time.Second
	idleTimeout          = 60 * time.Second
	httpShutdownTimeout  = 10 * time.Second
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

	if err := os.MkdirAll(config.UploadDirectory, 0755); err != nil {
		log.Printf("Unable to create upload directory: %v", err)
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	savedFilename, err := saveUploadedFile(config.UploadDirectory, filename, file)
	if err != nil {
		log.Printf("Unable to save upload file: %v", err)
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "File uploaded successfully: %s\n", savedFilename)
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

type tcpServer struct {
	listener   net.Listener
	mu         sync.Mutex
	conns      map[net.Conn]struct{}
	handlers   sync.WaitGroup
	acceptDone chan struct{}
	closing    bool
}

func startTCPServer(address string) (*tcpServer, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("start TCP server on %s: %w", address, err)
	}

	server := &tcpServer{
		listener:   listener,
		conns:      make(map[net.Conn]struct{}),
		acceptDone: make(chan struct{}),
	}
	log.Println("GoServe TCP server running on", listener.Addr())
	go server.acceptConnections()
	return server, nil
}

func (server *tcpServer) acceptConnections() {
	defer close(server.acceptDone)
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			server.mu.Lock()
			closing := server.closing
			server.mu.Unlock()
			if !closing {
				log.Println("TCP connection error:", err)
			}
			return
		}

		server.mu.Lock()
		if server.closing {
			server.mu.Unlock()
			_ = conn.Close()
			continue
		}
		server.conns[conn] = struct{}{}
		server.handlers.Add(1)
		server.mu.Unlock()

		go func() {
			defer server.handlers.Done()
			defer func() {
				server.mu.Lock()
				delete(server.conns, conn)
				server.mu.Unlock()
			}()
			handleTCPConnection(conn)
		}()
	}
}

func (server *tcpServer) shutdown() error {
	server.mu.Lock()
	server.closing = true
	var shutdownErr error
	if err := server.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close TCP listener: %w", err))
	}
	for conn := range server.conns {
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close TCP client: %w", closeErr))
		}
	}
	server.mu.Unlock()

	<-server.acceptDone
	server.handlers.Wait()
	return shutdownErr
}

func shutdownHTTPServer(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		closeErr := server.Close()
		if closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			return errors.Join(fmt.Errorf("gracefully shut down HTTPS server: %w", err), fmt.Errorf("force-close HTTPS server: %w", closeErr))
		}
		return fmt.Errorf("gracefully shut down HTTPS server: %w", err)
	}
	return nil
}

func run(ctx context.Context, args []string) error {
	config, err := parseServerConfig(args)
	if err != nil {
		return err
	}

	tcp, err := startTCPServer(fmt.Sprintf(":%d", config.TCPPort))
	if err != nil {
		return err
	}

	server := newHTTPServer(config)
	fmt.Printf("GoServe running on https://localhost%s\n", server.Addr)
	fmt.Printf("Serving directory: %s\n", config.StaticDirectory)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ListenAndServeTLS("cert/server.crt", "cert/server.key")
	}()

	select {
	case <-ctx.Done():
		tcpErr := tcp.shutdown()
		httpErr := shutdownHTTPServer(server)
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr = errors.Join(httpErr, fmt.Errorf("HTTPS server stopped: %w", err))
		}
		return errors.Join(tcpErr, httpErr)
	case err := <-serveErr:
		tcpErr := tcp.shutdown()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.Join(fmt.Errorf("HTTPS server stopped: %w", err), tcpErr)
		}
		return tcpErr
	}
}

func main() {
	if err := runWithSignals(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runWithSignals(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return run(ctx, args)
}
