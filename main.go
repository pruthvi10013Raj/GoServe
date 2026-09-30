package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

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

	err := r.ParseMultipartForm(10 << 20)

	if err != nil {
		http.Error(w, "Unable to parse uploaded file", http.StatusBadRequest)
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

	fmt.Fprintf(w, "File uploaded successfully: %s\n", filename)
}

func main() {

	port := flag.Int("port", 8080, "Port on which server will run")
	directory := flag.String("dir", "./public", "Directory to serve")

	flag.Parse()

	fileServer := http.FileServer(http.Dir(*directory))

	handler := loggingMiddleware(fileServer)

	http.Handle("/", handler)

	http.HandleFunc("/upload", uploadHandler)

	address := fmt.Sprintf(":%d", *port)

	fmt.Printf("GoServe running on http://localhost%s\n", address)
	fmt.Printf("Serving directory: %s\n", *directory)

	err := http.ListenAndServe(address, nil)

	if err != nil {
		fmt.Println("Server stopped:", err)
	}
}
