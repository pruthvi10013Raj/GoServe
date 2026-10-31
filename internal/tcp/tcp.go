package tcp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	MaxRequestSize    = 1024
	connectionTimeout = 5 * time.Second
)

func HandleConnection(conn net.Conn) {
	handleConnectionWithTimeout(conn, connectionTimeout)
}

func handleConnectionWithTimeout(conn net.Conn, timeout time.Duration) {
	defer conn.Close()

	log.Println("TCP client connected:", conn.RemoteAddr())
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		log.Println("TCP read deadline error:", err)
		return
	}

	reader := bufio.NewReaderSize(conn, MaxRequestSize+2)
	request, err := reader.ReadSlice('\n')
	if err == bufio.ErrBufferFull || len(request) > MaxRequestSize+2 {
		writeTCPError(conn, timeout)
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
	if len(request) > MaxRequestSize {
		writeTCPError(conn, timeout)
		return
	}

	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		log.Println("TCP write deadline error:", err)
		return
	}
	if _, err := fmt.Fprintf(conn, "Received: %s\n", request); err != nil {
		log.Println("TCP write error:", err)
	}
}

func writeTCPError(conn net.Conn, timeout time.Duration) {
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		log.Println("TCP write deadline error:", err)
		return
	}
	if _, err := io.WriteString(conn, "ERR request too long\n"); err != nil {
		log.Println("TCP write error:", err)
	}
}

type Server struct {
	listener   net.Listener
	mu         sync.Mutex
	conns      map[net.Conn]struct{}
	handlers   sync.WaitGroup
	acceptDone chan struct{}
	closing    bool
}

func StartServer(address string) (*Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("start TCP server on %s: %w", address, err)
	}

	server := &Server{
		listener:   listener,
		conns:      make(map[net.Conn]struct{}),
		acceptDone: make(chan struct{}),
	}
	log.Println("GoServe TCP server running on", listener.Addr())
	go server.acceptConnections()
	return server, nil
}

func (server *Server) Addr() net.Addr {
	return server.listener.Addr()
}

func (server *Server) acceptConnections() {
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
			HandleConnection(conn)
		}()
	}
}

func (server *Server) Shutdown() error {
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
