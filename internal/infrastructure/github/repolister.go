package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var ErrNoMarkdown = errors.New("no .md files found in repo/path")

// RepoLister lists the raw URLs of every Markdown file in a GitHub repo (or a
// subpath of it) via the git trees API.
type RepoLister struct {
	client  *http.Client
	apiBase string
	rawBase string
	token   string
}

func NewRepoLister() *RepoLister {
	return &RepoLister{
		client:  &http.Client{Timeout: 15 * time.Second},
		apiBase: "https://api.github.com",
		rawBase: "https://raw.githubusercontent.com",
		token:   os.Getenv("GITHUB_TOKEN"),
	}
}

// NewRepoListerWithBases is for tests: it injects the API/raw base URLs.
func NewRepoListerWithBases(client *http.Client, apiBase, rawBase, token string) *RepoLister {
	return &RepoLister{client: client, apiBase: apiBase, rawBase: rawBase, token: token}
}

// ListMarkdown returns the raw URLs of every .md file in the repo at repoURL,
// restricted to its subpath when the URL includes one.
func (l *RepoLister) ListMarkdown(ctx context.Context, repoURL string) ([]string, error) {
	owner, repo, ref, subpath, err := parseRepoURL(repoURL)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		if ref, err = l.defaultBranch(ctx, owner, repo); err != nil {
			return nil, err
		}
	}

	paths, err := l.treePaths(ctx, owner, repo, ref)
	if err != nil {
		return nil, err
	}

	prefix := ""
	if subpath != "" {
		prefix = strings.Trim(subpath, "/") + "/"
	}
	var urls []string
	for _, p := range paths {
		if !strings.HasSuffix(strings.ToLower(p), ".md") {
			continue
		}
		if prefix != "" && !strings.HasPrefix(p, prefix) {
			continue
		}
		urls = append(urls, l.rawBase+"/"+owner+"/"+repo+"/"+ref+"/"+p)
	}
	if len(urls) == 0 {
		return nil, ErrNoMarkdown
	}
	return urls, nil
}

// parseRepoURL accepts https://github.com/owner/repo[/tree/<ref>/<subpath>] or the
// shorthand owner/repo, returning owner, repo, ref ("" = default branch) and subpath.
func parseRepoURL(input string) (owner, repo, ref, subpath string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", "", "", ErrEmptyURL
	}

	path := input
	if strings.Contains(input, "://") {
		parsed, perr := url.Parse(input)
		if perr != nil {
			return "", "", "", "", fmt.Errorf("parse url: %w", perr)
		}
		if parsed.Host != "github.com" && parsed.Host != "www.github.com" {
			return "", "", "", "", ErrUnsupportedURL
		}
		path = parsed.Path
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", "", ErrUnsupportedURL
	}
	owner = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")
	if len(parts) >= 4 && parts[2] == "tree" {
		ref = parts[3]
		subpath = strings.Join(parts[4:], "/")
	}
	return owner, repo, ref, subpath, nil
}

func (l *RepoLister) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	var out struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := l.getJSON(ctx, l.apiBase+"/repos/"+owner+"/"+repo, &out); err != nil {
		return "", err
	}
	if out.DefaultBranch == "" {
		return "", fmt.Errorf("could not resolve default branch for %s/%s", owner, repo)
	}
	return out.DefaultBranch, nil
}

func (l *RepoLister) treePaths(ctx context.Context, owner, repo, ref string) ([]string, error) {
	var out struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	endpoint := l.apiBase + "/repos/" + owner + "/" + repo + "/git/trees/" + url.PathEscape(ref) + "?recursive=1"
	if err := l.getJSON(ctx, endpoint, &out); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(out.Tree))
	for _, entry := range out.Tree {
		if entry.Type == "blob" {
			paths = append(paths, entry.Path)
		}
	}
	return paths, nil
}

func (l *RepoLister) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if l.token != "" {
		req.Header.Set("Authorization", "Bearer "+l.token)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("github api %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github api %s: status %d", endpoint, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
