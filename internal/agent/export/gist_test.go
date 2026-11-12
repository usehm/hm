// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package export

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateGist(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		scopes  string // "" sends no X-OAuth-Scopes header
		wantErr error
	}{
		{"created", http.StatusCreated, "repo, gist", nil},
		{"classic token without gist", http.StatusNotFound, "repo", ErrGistScope},
		{"fine-grained without gists", http.StatusForbidden, "", ErrGistScope},
		{"bad token", http.StatusUnauthorized, "", ErrBadToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/gists" || r.Header.Get("Authorization") != "Bearer tok" {
					t.Errorf("request %s %s auth %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
				}
				var body struct {
					Public bool
					Files  map[string]struct{ Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Public || body.Files["a.html"].Content != "<p>hi</p>" {
					t.Errorf("body %+v, %v", body, err)
				}
				if tt.scopes != "" {
					w.Header().Set("X-OAuth-Scopes", tt.scopes)
				}
				w.WriteHeader(tt.status)
				if tt.status == http.StatusCreated {
					_, _ = w.Write([]byte(`{"id":"abc123","html_url":"https://gist.github.com/me/abc123","owner":{"login":"me"},
						"files":{"a.html":{"raw_url":"https://gist.githubusercontent.com/me/abc123/raw/a.html"}}}`))
				} else {
					_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				}
			}))
			defer srv.Close()
			g, err := CreateGist(context.Background(), srv.Client(), srv.URL, "tok", "a.html", "desc", []byte("<p>hi</p>"))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && (g.ID != "abc123" || g.Owner != "me" || g.URL != "https://gist.github.com/me/abc123") {
				t.Fatalf("gist %+v", g)
			}
		})
	}
}

func TestViewerURL(t *testing.T) {
	g := Gist{ID: "abc", URL: "https://gist.github.com/me/abc", Owner: "me", File: "s.html"}
	tests := map[string]string{
		"":                                "https://gist.github.com/me/abc",
		"https://view.example/?gist={id}": "https://view.example/?gist=abc",
		"https://view.example/{user}/{id}/{file}": "https://view.example/me/abc/s.html",
	}
	for viewer, want := range tests {
		if got := ViewerURL(viewer, g); got != want {
			t.Errorf("ViewerURL(%q) = %q, want %q", viewer, got, want)
		}
	}
}
