package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// ReadinessProbe is a standalone liveness flag decoupled from the HTTP server.
// Pass one instance to both handler.New (for /readyz) and the composition root
// (to call MarkReady after startup). This breaks the circular dependency that
// would arise from handler needing the server and server needing the handler.
type ReadinessProbe struct {
	ready atomic.Bool
}

// IsReady reports whether MarkReady has been called.
func (p *ReadinessProbe) IsReady() bool { return p.ready.Load() }

// MarkReady signals that the server has completed startup.
func (p *ReadinessProbe) MarkReady() { p.ready.Store(true) }

// Server wraps an http.Server and adds graceful shutdown.
type Server struct {
	addr    string
	handler http.Handler
}

// New creates a Server that listens on addr with the given handler.
func New(addr string, handler http.Handler) *Server {
	return &Server{addr: addr, handler: handler}
}

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
