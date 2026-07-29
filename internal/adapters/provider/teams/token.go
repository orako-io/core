// SPDX-License-Identifier: AGPL-3.0-or-later

package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// botFrameworkScope is the AAD v2 client-credentials scope for the Bot
// Framework connector API (Verified 2026-07-06: resource/scope
// https://api.botframework.com).
const botFrameworkScope = "https://api.botframework.com/.default"

// tokenExpiryMargin is subtracted from the AAD-reported expiry so a cached
// token is never handed out with less than this much life left.
const tokenExpiryMargin = 60 * time.Second

// tokenSource obtains and caches the AAD client-credentials bearer token used
// to call the Bot Framework connector REST API. Safe for concurrent use.
type tokenSource struct {
	cfg    Config
	client *http.Client

	mu        sync.Mutex `exhaustruct:"optional"`
	cached    string     `exhaustruct:"optional"`
	expiresAt time.Time  `exhaustruct:"optional"`
}

// newTokenSource builds a tokenSource for cfg.
func newTokenSource(cfg Config) *tokenSource {
	return &tokenSource{cfg: cfg, client: cfg.httpClient()}
}

// aadTokenResponse is the relevant subset of the AAD v2 token endpoint's JSON
// response.
type aadTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// Token returns a valid bearer token, fetching and caching a new one when the
// cached token is absent or within tokenExpiryMargin of expiring. The AAD
// network fetch runs with the mutex released — only the "do we need a
// refresh" check and the eventual cache write are held under lock — so one
// stalled AAD endpoint blocks only its own caller, not every concurrent
// Token() call. Two callers can therefore race to fetch concurrently when
// the cache is simultaneously empty/expired; both requests succeed and the
// last write wins, which is cheaper than serializing every caller on the
// network.
func (t *tokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()

	if t.cached != "" && time.Now().Before(t.expiresAt) {
		cached := t.cached

		t.mu.Unlock()

		return cached, nil
	}

	t.mu.Unlock()

	token, expiresIn, err := t.fetch(ctx)
	if err != nil {
		return "", err
	}

	t.mu.Lock()
	t.cached = token
	t.expiresAt = time.Now().Add(time.Duration(expiresIn)*time.Second - tokenExpiryMargin)
	t.mu.Unlock()

	return token, nil
}

// fetch performs the AAD v2 client-credentials grant.
func (t *tokenSource) fetch(ctx context.Context) (string, int64, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", t.cfg.ClientID)
	form.Set("client_secret", t.cfg.ClientSecret)
	form.Set("scope", botFrameworkScope)

	endpoint := t.cfg.aadBase() + "/" + t.cfg.TenantID + "/oauth2/v2.0/token"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("teams token: building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("teams token: requesting AAD token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	var out aadTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, fmt.Errorf("teams token: decoding AAD response: %w", err)
	}

	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		if out.Error != "" {
			return "", 0, fmt.Errorf("teams token: AAD error %s: %s", out.Error, out.ErrorDesc)
		}

		return "", 0, fmt.Errorf("teams token: AAD token request failed with status %d", resp.StatusCode)
	}

	return out.AccessToken, out.ExpiresIn, nil
}
