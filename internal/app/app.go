package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"goserve/internal/config"
	"goserve/internal/httpserver"
	"goserve/internal/tcp"
)

const httpShutdownTimeout = 10 * time.Second

func Run(ctx context.Context, args []string) error {
	return runWithHTTPS(ctx, args, os.Getenv, func(server *http.Server) error {
		return server.ListenAndServeTLS("cert/server.crt", "cert/server.key")
	})
}

func runWithHTTPS(ctx context.Context, args []string, getenv func(string) string, serveHTTPS func(*http.Server) error) error {
	serverConfig, err := config.Parse(args)
	if err != nil {
		return err
	}
	credentials, err := httpserver.CredentialsFromEnvironment(getenv)
	if err != nil {
		return err
	}

	tcpServer, err := tcp.StartServer(fmt.Sprintf(":%d", serverConfig.TCPPort))
	if err != nil {
		return err
	}

	server := httpserver.NewServer(httpserver.ServerConfig{
		Config:     serverConfig,
		UploadAuth: credentials,
	})
	fmt.Printf("Starting GoServe HTTPS on https://localhost%s\n", server.Addr)
	fmt.Printf("Serving directory: %s\n", serverConfig.StaticDirectory)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serveHTTPS(server)
	}()

	select {
	case <-ctx.Done():
		tcpErr := tcpServer.Shutdown()
		httpErr := shutdownHTTPServer(server)
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr = errors.Join(httpErr, fmt.Errorf("HTTPS server stopped: %w", err))
		}
		return errors.Join(tcpErr, httpErr)
	case err := <-serveErr:
		tcpErr := tcpServer.Shutdown()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.Join(fmt.Errorf("HTTPS server stopped: %w", err), tcpErr)
		}
		return tcpErr
	}
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
