package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHubAPI is where gists are created.
const GitHubAPI = "https://api.github.com"

// ErrGistScope means the token may not create gists: a classic token
// without the gist scope, or a fine-grained one without Gists access.
var ErrGistScope = errors.New("the GitHub token cannot create gists; it needs the gist scope")

// ErrBadToken means GitHub did not accept the token.
var ErrBadToken = errors.New("GitHub did not accept the token")

// Gist is a created gist.
type Gist struct {
	ID      string
	URL     string // the gist's page
	Owner   string
	File    string
	RawURL  string
	Created time.Time
}

// CreateGist uploads one file as a secret gist. api is GitHubAPI, or a
// test server.
func CreateGist(ctx context.Context, client *http.Client, api, token, file, description string, content []byte) (Gist, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	body, err := json.Marshal(map[string]any{
		"description": description,
		"public":      false,
		"files":       map[string]any{file: map[string]string{"content": string(content)}},
	})
	if err != nil {
		return Gist{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(api, "/")+"/gists", bytes.NewReader(body))
	if err != nil {
		return Gist{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return Gist{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
	case http.StatusUnauthorized:
		return Gist{}, ErrBadToken
	case http.StatusForbidden, http.StatusNotFound:
		// GitHub answers 404 to a classic token without the gist scope.
		if scopes, ok := resp.Header["X-Oauth-Scopes"]; !ok || !strings.Contains(strings.Join(scopes, ","), "gist") {
			return Gist{}, ErrGistScope
		}
		fallthrough
	default:
		var e struct{ Message string }
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = resp.Status
		}
		return Gist{}, fmt.Errorf("GitHub: %s", e.Message)
	}
	var out struct {
		ID      string `json:"id"`
		HTMLURL string `json:"html_url"`
		Owner   struct {
			Login string `json:"login"`
		} `json:"owner"`
		Files map[string]struct {
			RawURL string `json:"raw_url"`
		} `json:"files"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Gist{}, fmt.Errorf("GitHub's answer: %w", err)
	}
	if out.ID == "" {
		return Gist{}, errors.New("GitHub's answer has no gist ID")
	}
	return Gist{ID: out.ID, URL: out.HTMLURL, Owner: out.Owner.Login, File: file, RawURL: out.Files[file].RawURL,
		Created: out.CreatedAt}, nil
}

// ViewerURL is the link to hand out for g: viewer with {id}, {user} and
// {file} filled in, or the gist's own page when viewer is empty.
func ViewerURL(viewer string, g Gist) string {
	if viewer == "" {
		return g.URL
	}
	return strings.NewReplacer("{id}", g.ID, "{user}", g.Owner, "{file}", g.File).Replace(viewer)
}
