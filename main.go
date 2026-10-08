package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
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

func uploadHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	const maxUploadSize = 10 << 20 // 10 MB

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	err := r.ParseMultipartForm(maxUploadSize)

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

	outputPath := filepath.Join("uploads", filename)

	// Handle duplicate filenames
	extension := filepath.Ext(filename)
	name := filename[:len(filename)-len(extension)]

	counter := 1

	for {
		if _, err := os.Stat(outputPath); os.IsNotExist(err) {
			break
		}

		newFilename := fmt.Sprintf("%s_%d%s", name, counter, extension)

		outputPath = filepath.Join("uploads", newFilename)

		counter++
	}

	outputFile, err := os.Create(outputPath)

	if err != nil {
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	defer outputFile.Close()

	_, err = io.Copy(outputFile, file)

	if err != nil {
		http.Error(w, "Unable to write file", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "File uploaded successfully: %s\n", filepath.Base(outputPath))
}

func handleTCPConnection(conn net.Conn) {

	defer conn.Close()

	log.Println("TCP client connected:", conn.RemoteAddr())

	message := "Hello from GoServe TCP server!\n"

	_, err := conn.Write([]byte(message))

	if err != nil {
		log.Println("TCP write error:", err)
		return
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

func main() {

	port := flag.Int("port", 8443, "Port on which server will run")
	directory := flag.String("dir", "./public", "Directory to serve")

	flag.Parse()

	go startTCPServer(":9090")

	fileServer := http.FileServer(http.Dir(*directory))

	handler := loggingMiddleware(fileServer)

	http.Handle("/", handler)

	uploadHandlerWithAuth := basicAuth(http.HandlerFunc(uploadHandler))

	http.Handle("/upload", uploadHandlerWithAuth)

	address := fmt.Sprintf(":%d", *port)

	fmt.Printf("GoServe running on https://localhost%s\n", address)
	fmt.Printf("Serving directory: %s\n", *directory)

	err := http.ListenAndServeTLS(
		address,
		"cert/server.crt",
		"cert/server.key",
		nil,
	)

	if err != nil {
		fmt.Println("Server stopped:", err)
	}
}
