// SPDX-License-Identifier: AGPL-3.0-or-later

package teams

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestTokenSource_Fetch_TimesOutAgainstHangingServer proves the token fetch
// is bounded by the configured HTTP client timeout rather than blocking
// forever against a stalled AAD endpoint (review finding #8).
func TestTokenSource_Fetch_TimesOutAgainstHangingServer(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-block // hang until the test unblocks it
	}))
	t.Cleanup(func() {
		close(block)
		server.Close()
	})

	src := newTokenSource(Config{
		TenantID:     "tenant",
		ClientID:     "client",
		ClientSecret: "secret",
		AADBaseURL:   server.URL,
		// A short client timeout stands in for defaultHTTPTimeout so the
		// test doesn't have to wait out the full production budget to prove
		// the fetch is bounded at all.
		HTTPClient: &http.Client{Timeout: 50 * time.Millisecond},
	})

	start := time.Now()

	if _, err := src.Token(t.Context()); err == nil {
		t.Fatal("want a timeout error against a hanging AAD endpoint, got nil")
	}

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Token() took %v to return after the client timeout, want well under 2s", elapsed)
	}
}

// TestTokenSource_Token_ConcurrentCallsDoNotSerializeOnNetwork proves two
// concurrent Token() calls on the same tokenSource both reach the network
// concurrently rather than being serialized behind the cache mutex (review
// finding #8: the old code held the mutex across the AAD fetch). The fake
// AAD server only responds once it has observed two simultaneously in-flight
// requests; under the old, mutex-across-fetch code the second call would
// never even reach the server until the first fetch finished, so this test
// would time out.
func TestTokenSource_Token_ConcurrentCallsDoNotSerializeOnNetwork(t *testing.T) {
	t.Parallel()

	const callers = 2

	var (
		mu       sync.Mutex
		inFlight int
	)

	bothArrived := make(chan struct{})

	var closeOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		inFlight++
		reachedTarget := inFlight == callers
		mu.Unlock()

		if reachedTarget {
			closeOnce.Do(func() { close(bothArrived) })
		}

		select {
		case <-bothArrived:
		case <-time.After(2 * time.Second):
			t.Error("Token() calls did not reach the network concurrently — still serializing on the fetch")
		}

		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	t.Cleanup(server.Close)

	src := newTokenSource(Config{
		TenantID:     "tenant",
		ClientID:     "client",
		ClientSecret: "secret",
		AADBaseURL:   server.URL,
	})

	var wg sync.WaitGroup

	errs := make([]error, callers)

	for i := range callers {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_, err := src.Token(t.Context())
			errs[i] = err
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Token() call %d: %v", i, err)
		}
	}
}
