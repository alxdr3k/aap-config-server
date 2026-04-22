package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Server wraps an http.Server and adds graceful shutdown + readiness tracking.
type Server struct {
	addr    string
	handler http.Handler
	ready   atomic.Bool
}

// New creates a Server that listens on addr with the given handler.
// handler may be nil and set later via SetHandler before Run is called.
func New(addr string, handler http.Handler) *Server {
	return &Server{addr: addr, handler: handler}
}

// SetHandler sets the HTTP handler. Must be called before Run.
func (s *Server) SetHandler(h http.Handler) { s.handler = h }

// IsReady reports whether the server has completed its startup sequence.
// Implements handler.Readiness.
func (s *Server) IsReady() bool { return s.ready.Load() }

// MarkReady signals that the server is ready to serve traffic.
func (s *Server) MarkReady() { s.ready.Store(true) }

// Run starts the HTTP server and blocks until ctx is cancelled (SIGINT/SIGTERM).
// After cancellation it performs a graceful shutdown with a 10-second timeout.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.handler,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("HTTP server starting", "addr", s.addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("listen: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		slog.Info("HTTP server stopped")
		return nil
	}
}
