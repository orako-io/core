// SPDX-License-Identifier: Apache-2.0

// Package server provides a graceful HTTP server helper for Orako binaries.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Timeouts and limits applied to every server built by [Run].
const (
	readHeaderTimeout = 5 * time.Second
	// readTimeout bounds the whole request read (headers + body), defeating a
	// slow-body connection hold. Generous enough for the largest legitimate
	// body (a bounded ask context packet).
	readTimeout = 30 * time.Second
	// idleTimeout bounds an idle keep-alive connection.
	idleTimeout = 120 * time.Second
	// maxHeaderBytes caps request headers (the body is capped by the
	// MaxBytesReader middleware). No WriteTimeout is set on purpose: the SSE
	// realtime stream (/events/stream) is a deliberately long-lived response.
	maxHeaderBytes  = 1 << 20
	shutdownTimeout = 10 * time.Second
)

// Run starts an HTTP server on addr and blocks until ctx is cancelled.
// The server accepts both HTTP/1 and unencrypted HTTP/2 (h2c) connections so
// Connect-RPC server-streaming works over plain TCP without TLS.
//
// On cancellation it attempts a graceful shutdown bounded by a fixed timeout.
// It returns nil on a clean shutdown and a non-nil error if the server failed
// to start or could not drain in time.
func Run(ctx context.Context, log *slog.Logger, addr string, handler http.Handler) error {
	var protos http.Protocols

	protos.SetHTTP1(true)
	protos.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		Protocols:         &protos,
	}

	errCh := make(chan error, 1)

	go func() {
		log.InfoContext(ctx, "http server listening", slog.String("addr", addr))

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)

			return
		}

		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return shutdown(srv, log)
	}
}

// shutdown drains srv within shutdownTimeout.
func shutdown(srv *http.Server, log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	log.InfoContext(ctx, "http server shutting down")

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("draining http server: %w", err)
	}

	return nil
}
