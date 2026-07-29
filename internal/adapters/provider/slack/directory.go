// SPDX-License-Identifier: AGPL-3.0-or-later

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ErrUserNotFound is returned by Directory.LookupByEmail when no Slack workspace
// user matches the given email address.
var ErrUserNotFound = errors.New("slack directory: user not found for email")

// Directory looks up Slack workspace users for member seeding.
//
// An admin uses this to bind a responder's Orako member record to their Slack
// user ID by email, satisfying AddMember without manual data entry.
// The match key is email, consistent with the identity PRD (§3).
//
// LookupByEmail calls Slack's users.lookupByEmail API method, which is more
// efficient than paginating users.list for sparse lookups.
//
// Callers must hold a bot token with the users:read.email scope.
type Directory interface {
	// LookupByEmail returns the Slack user ID for the given email.
	// Returns ErrUserNotFound when no workspace user matches.
	LookupByEmail(ctx context.Context, email string) (string, error)
}

// APIDirectory is the real Slack-backed Directory implementation.
// It uses users.lookupByEmail for O(1) lookups (no pagination required).
type APIDirectory struct {
	botToken string
	baseURL  string // override for tests; empty uses "https://slack.com/api"
	client   *http.Client
}

// NewAPIDirectory builds an APIDirectory backed by the given bot token.
// baseURL is empty in production (defaulting to the Slack Web API);
// tests pass an httptest server URL.
func NewAPIDirectory(botToken, baseURL string, client *http.Client) *APIDirectory {
	if client == nil {
		client = newHTTPClient()
	}

	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}

	return &APIDirectory{botToken: botToken, baseURL: baseURL, client: client}
}

// LookupByEmail calls Slack's users.lookupByEmail and returns the Slack user ID.
// Returns ErrUserNotFound when the email is not in the workspace.
func (d *APIDirectory) LookupByEmail(ctx context.Context, email string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		d.baseURL+"/users.lookupByEmail?"+url.Values{"email": {email}}.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("building lookupByEmail request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+d.botToken)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("users.lookupByEmail: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		User  struct {
			ID string `json:"id"`
		} `json:"user"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding lookupByEmail response: %w", err)
	}

	if !result.OK {
		if result.Error == "users_not_found" {
			return "", ErrUserNotFound
		}

		return "", fmt.Errorf("slack users.lookupByEmail: %s", result.Error)
	}

	return result.User.ID, nil
}
