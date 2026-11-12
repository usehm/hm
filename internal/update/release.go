// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Copied from chapar internal/update/release.go (github.com/chapar-rest/chapar@2342e52a).

// Package update finds out whether a newer hm release is out, and
// downloads, verifies and installs it where the platform allows.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// LatestURL is the GitHub API endpoint for the newest published release.
// Drafts and prereleases never show up there.
const LatestURL = "https://api.github.com/repos/usehm/hm/releases/latest"

// Release is a published hm release.
type Release struct {
	Version string // tag, e.g. "v0.12.0"
	URL     string // release page, where the notes are
	Notes   string // markdown body
	Assets  []Asset
}

// Asset is one file attached to a release.
type Asset struct {
	Name string
	URL  string // direct download
	Size int64
	// Digest is GitHub's "sha256:<hex>" for the file, empty on old releases.
	Digest string
}

// Asset returns the release asset with the given name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Client fetches release info and assets. The zero value uses LatestURL and
// an HTTP client that honors the proxy environment variables.
type Client struct {
	HTTP      *http.Client
	LatestURL string
	UserAgent string
	// Repo, as "owner/name", picks the GitHub repository instead of
	// LatestURL's.
	Repo string
	// Prerelease also offers prereleases.
	Prerelease bool
}

// ghRelease is a release in the GitHub API.
type ghRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func (b ghRelease) release() Release {
	r := Release{Version: b.TagName, URL: b.HTMLURL, Notes: b.Body}
	for _, a := range b.Assets {
		r.Assets = append(r.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size, Digest: a.Digest})
	}
	return r
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
}

// Latest returns the newest published release, or with Prerelease the
// newest release of any kind.
func (c Client) Latest(ctx context.Context) (Release, error) {
	url := c.LatestURL
	switch {
	case c.Repo != "" && c.Prerelease:
		url = "https://api.github.com/repos/" + c.Repo + "/releases?per_page=30"
	case c.Repo != "":
		url = "https://api.github.com/repos/" + c.Repo + "/releases/latest"
	case url == "":
		url = LatestURL
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("checking for updates: %s", resp.Status)
	}
	var list []ghRelease
	if c.Repo != "" && c.Prerelease {
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			return Release{}, fmt.Errorf("checking for updates: %w", err)
		}
	} else {
		var one ghRelease
		if err := json.NewDecoder(resp.Body).Decode(&one); err != nil {
			return Release{}, fmt.Errorf("checking for updates: %w", err)
		}
		list = []ghRelease{one}
	}
	var best *ghRelease
	for i := range list {
		b := &list[i]
		if b.Draft {
			continue
		}
		v, ok := parseVersion(b.TagName)
		if !ok {
			continue
		}
		if best == nil {
			best = b
			continue
		}
		if bv, _ := parseVersion(best.TagName); compare(bv, v) < 0 {
			best = b
		}
	}
	if best == nil {
		if len(list) == 1 {
			return Release{}, fmt.Errorf("checking for updates: unexpected tag %q", list[0].TagName)
		}
		return Release{}, fmt.Errorf("checking for updates: no releases")
	}
	return best.release(), nil
}

// Newer reports whether latest is a later version than current. A current
// version that is not a release (a dev build) is never behind.
func Newer(current, latest string) bool {
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	return compare(c, l) < 0
}

// IsRelease reports whether v is a release version like "v1.2.3".
func IsRelease(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

type version struct {
	nums [3]int
	pre  string // "rc.1" in v1.2.3-rc.1
}

func parseVersion(s string) (version, bool) {
	s, ok := strings.CutPrefix(s, "v")
	if !ok {
		return version{}, false
	}
	var v version
	s, v.pre, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.nums[i] = n
	}
	return v, true
}

// compare orders versions; a prerelease sorts before its release.
func compare(a, b version) int {
	for i := range a.nums {
		if a.nums[i] != b.nums[i] {
			if a.nums[i] < b.nums[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	}
	return strings.Compare(a.pre, b.pre)
}

// ErrUnsupported means this copy of hm cannot update itself; the user
// downloads the new version instead.
var ErrUnsupported = errors.New("this copy of hm can't update itself")
