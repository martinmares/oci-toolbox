package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitLabClient deletes tags through the GitLab API, independently of the
// registry's permission to delete an entire manifest by digest.
type GitLabClient struct {
	baseURL      string
	token        string
	httpClient   *http.Client
	repositories map[string]int64
}

// NewGitLabClient shares the registry's timeout and TLS settings. Authentication
// uses PRIVATE-TOKEN, never the registry username or password.
func NewGitLabClient(cfg Config, project, token string) (*GitLabClient, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil || base == nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("gitlab.url must be an HTTP(S) URL without credentials, query, or fragment")
	}
	if strings.TrimSpace(project) == "" || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("gitlab.project and gitlab.token are required for cleanup.delete_via: gitlab")
	}
	transport, err := newTransport(cfg)
	if err != nil {
		return nil, err
	}
	return &GitLabClient{
		baseURL: base.String() + "/api/v4/projects/" + url.PathEscape(project) + "/registry/repositories",
		token:   token,
		httpClient: &http.Client{Timeout: cfg.Timeout, Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *GitLabClient) request(ctx context.Context, method, endpoint string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// RepositoryID resolves an exact repository path within the configured project.
func (c *GitLabClient) RepositoryID(ctx context.Context, repository string) (int64, error) {
	if c.repositories == nil {
		repositories := map[string]int64{}
		for page := 1; ; {
			resp, err := c.request(ctx, http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d", c.baseURL, page))
			if err != nil {
				return 0, err
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return 0, fmt.Errorf("GitLab list repositories: %s", resp.Status)
			}
			var entries []struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}
			err = json.NewDecoder(resp.Body).Decode(&entries)
			resp.Body.Close()
			if err != nil {
				return 0, fmt.Errorf("decode GitLab repositories: %w", err)
			}
			for _, entry := range entries {
				repositories[entry.Path] = entry.ID
			}
			next := resp.Header.Get("X-Next-Page")
			if next == "" {
				break
			}
			nextPage, err := strconv.Atoi(next)
			if err != nil || nextPage <= page {
				return 0, fmt.Errorf("invalid GitLab pagination")
			}
			page = nextPage
		}
		c.repositories = repositories
	}
	id, ok := c.repositories[repository]
	if !ok || id <= 0 {
		return 0, fmt.Errorf("repository %q not found in configured GitLab project", repository)
	}
	return id, nil
}

// DeleteTag removes a tag through GitLab API. The caller verifies its digest
// through Registry API; GitLab's tag detail endpoint can return a false 404.
func (c *GitLabClient) DeleteTag(ctx context.Context, repository, tag string) error {
	id, err := c.RepositoryID(ctx, repository)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/%d/tags/%s", c.baseURL, id, url.PathEscape(tag))

	resp, err := c.request(ctx, http.MethodDelete, endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("GitLab delete tag %s:%s: %s", repository, tag, resp.Status)
	}
}
