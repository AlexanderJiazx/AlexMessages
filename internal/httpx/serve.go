package httpx

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Serve runs h on addr until the process receives SIGINT/SIGTERM, then drains
// in-flight requests within a short grace period before returning.
//
// Timeouts are chosen so long-lived endpoints keep working: ReadHeaderTimeout
// and IdleTimeout guard against slow-loris/leaked connections, but there is
// deliberately no WriteTimeout — that would kill the WebSocket control plane
// and the debug-console SSE stream. name is used only for log lines.
func Serve(name, addr string, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Run the listener in the background so the main goroutine can wait for a
	// shutdown signal.
	errCh := make(chan error, 1)
	go func() {
		log.Printf("[%s] listening on %s", name, addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Printf("[%s] %s received, shutting down", name, sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}
