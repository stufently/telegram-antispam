package ops

import (
	"context"
	"net/http"
	"time"
)

// handler builds the ops HTTP mux: /healthz for liveness checks and
// /metrics for Prometheus text-exposition scraping. Factored out so tests
// can exercise routes without binding a port.
func handler(reg *Registry) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		reg.Write(w)
	})

	return mux
}

// Server wraps an *http.Server exposing the ops endpoints.
type Server struct {
	srv *http.Server
}

// NewServer builds a Server listening on addr, serving reg's metrics.
func NewServer(addr string, reg *Registry) *Server {
	return &Server{srv: &http.Server{
		Addr:              addr,
		Handler:           handler(reg),
		ReadHeaderTimeout: 5 * time.Second,
	}}
}

// Run starts the server and blocks until ctx is done, at which point it
// gracefully shuts down. It returns nil on clean shutdown (including a
// clean stop of ListenAndServe itself), or a non-nil error if the server
// fails to bind/serve.
func (s *Server) Run(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() {
		errc <- s.srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
		return nil
	}
}
