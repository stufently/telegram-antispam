package ops

import (
	"context"
	"net/http"
	"time"
)

// handler builds the ops HTTP mux: /healthz for liveness checks and
// /metrics for Prometheus text-exposition scraping. Factored out so tests
// can exercise routes without binding a port.
func handler(reg *Registry, health *Health) http.Handler {
	mux := http.NewServeMux()

	// /healthz answers for the PROCESS, and is what readiness uses. It must
	// not depend on Telegram: readiness gates the Service, and dropping a
	// wedged pod out of the Service would also stop its /metrics from being
	// scraped — blinding the monitoring at the exact moment it is needed.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// /livez answers for the bot's ability to reach Telegram, and is what
	// liveness uses. Without it the old static /healthz reported a
	// perfectly healthy container while long polling was wedged or the
	// token had been revoked.
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if health == nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok (no probe wired)"))
			return
		}
		age := health.Age(time.Now())
		if !health.Live(time.Now()) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("no successful Telegram call for " + age.Truncate(time.Second).String()))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok, last Telegram call " + age.Truncate(time.Second).String() + " ago"))
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
// health may be nil, in which case /livez degrades to a static 200.
func NewServer(addr string, reg *Registry, health *Health) *Server {
	return &Server{srv: &http.Server{
		Addr:              addr,
		Handler:           handler(reg, health),
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
