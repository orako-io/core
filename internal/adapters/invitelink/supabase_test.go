// SPDX-License-Identifier: AGPL-3.0-or-later

package invitelink

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateInviteLinkHappyPath(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/admin/generate_link" {
			t.Errorf("path = %s", r.URL.Path)
		}

		if r.Header.Get("Authorization") != "Bearer service-key" {
			t.Errorf("missing bearer auth")
		}

		var body map[string]string

		_ = json.NewDecoder(r.Body).Decode(&body)

		if body["type"] != "invite" {
			t.Errorf("type = %s", body["type"])
		}

		_, _ = w.Write([]byte(`{"hashed_token":"tok123","verification_type":"invite"}`))
	}))

	defer srv.Close()

	token, linkType, err := NewSupabase(srv.URL, "service-key").GenerateInviteLink(t.Context(), "ada@example.com")
	if err != nil {
		t.Fatalf("GenerateInviteLink: %v", err)
	}

	if token != "tok123" || linkType != "invite" {
		t.Errorf("got (%q, %q), want (tok123, invite)", token, linkType)
	}
}

func TestGenerateInviteLinkNestedProperties(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"properties":{"hashed_token":"nested456"}}`))
	}))

	defer srv.Close()

	token, _, err := NewSupabase(srv.URL, "k").GenerateInviteLink(t.Context(), "ada@example.com")
	if err != nil {
		t.Fatalf("GenerateInviteLink: %v", err)
	}

	if token != "nested456" {
		t.Errorf("token = %q, want nested456", token)
	}
}

func TestGenerateInviteLinkFallsBackToMagiclink(t *testing.T) {
	t.Parallel()

	var types []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string

		_ = json.NewDecoder(r.Body).Decode(&body)
		types = append(types, body["type"])

		if body["type"] == "invite" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"msg":"A user with this email address has already been registered"}`))

			return
		}

		_, _ = w.Write([]byte(`{"hashed_token":"magic789"}`))
	}))

	defer srv.Close()

	token, linkType, err := NewSupabase(srv.URL, "k").GenerateInviteLink(t.Context(), "ada@example.com")
	if err != nil {
		t.Fatalf("GenerateInviteLink: %v", err)
	}

	if token != "magic789" || linkType != "magiclink" {
		t.Errorf("got (%q, %q), want (magic789, magiclink)", token, linkType)
	}

	if len(types) != 2 || types[0] != "invite" || types[1] != "magiclink" {
		t.Errorf("call sequence = %v", types)
	}
}

func TestGenerateInviteLinkAuthErrorNamesAPIOnly(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"msg":"invalid JWT"}`))
	}))

	defer srv.Close()

	_, _, err := NewSupabase(srv.URL, "the-secret-service-key").GenerateInviteLink(t.Context(), "ada@example.com")
	if err == nil {
		t.Fatal("expected an error on 401")
	}

	if !strings.Contains(err.Error(), "generate_link") {
		t.Errorf("error should name the admin API: %v", err)
	}

	if strings.Contains(err.Error(), "the-secret-service-key") {
		t.Errorf("error leaks the service key: %v", err)
	}
}
