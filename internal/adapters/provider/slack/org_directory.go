// SPDX-License-Identifier: AGPL-3.0-or-later

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// ErrNoDirectory means the project has no usable Slack provider (no org Slack
// connection, or no bot token), so no directory lookup is possible.
var ErrNoDirectory = errors.New("slack directory: no slack provider for project")

// orgCredLoader loads a project's org-level provider credentials JSON.
// *eventlog.OrgResolvingProviderLoader satisfies it.
type orgCredLoader interface {
	LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) ([]byte, error)
}

// OrgDirectory resolves a member's Slack user id by email using the Slack bot
// token stored for the project's org — the auto-bind path that saves an admin
// from ever entering a Slack id. It satisfies the command layer's
// chatDirectoryResolver.
type OrgDirectory struct {
	loader  orgCredLoader
	baseURL string       // test override; empty uses the Slack Web API
	client  *http.Client `exhaustruct:"optional"`
}

// NewOrgDirectory builds an OrgDirectory over the org credential loader.
func NewOrgDirectory(loader orgCredLoader) *OrgDirectory {
	return &OrgDirectory{loader: loader, baseURL: "", client: newHTTPClient()}
}

// LookupSlackByEmail loads the project's org Slack bot token and resolves the
// email to a Slack user id. Returns ErrNoDirectory when the org has no Slack
// provider/token, and slack.ErrUserNotFound when the email is not in the
// workspace — both of which the caller treats as "no binding".
func (d *OrgDirectory) LookupSlackByEmail(ctx context.Context, projectID uuid.UUID, email string) (string, error) {
	raw, err := d.loader.LoadProvider(ctx, projectID, "slack")
	if err != nil {
		return "", ErrNoDirectory
	}

	var creds struct {
		BotToken string `json:"bot_token"`
	}

	if err := json.Unmarshal(raw, &creds); err != nil || creds.BotToken == "" {
		return "", ErrNoDirectory
	}

	return NewAPIDirectory(creds.BotToken, d.baseURL, d.client).LookupByEmail(ctx, email)
}
