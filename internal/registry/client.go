package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json"

type Config struct {
	BaseURL            string
	Username           string
	Password           string
	Timeout            time.Duration
	InsecureSkipVerify bool
	CACertPath         string
}

type Client struct {
	baseURL    *url.URL
	username   string
	password   string
	httpClient *http.Client
	tokens     map[string]string
}

type ManifestInfo struct {
	Digest  string
	Blobs   []BlobInfo
	Created *time.Time
}

type BlobInfo struct {
	Digest string
	Size   int64
}

type NameUnknownError struct {
	Repository string
	Status     string
	Body       string
}

func (e NameUnknownError) Error() string {
	return fmt.Sprintf("repository %q not found: %s: %s", e.Repository, e.Status, e.Body)
}

func IsNameUnknown(err error) bool {
	var target NameUnknownError
	return errors.As(err, &target)
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil {
		return nil, err
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("registry URL must include scheme and host")
	}

	transport, err := newTransport(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{
		baseURL:    base,
		username:   cfg.Username,
		password:   cfg.Password,
		httpClient: &http.Client{Timeout: cfg.Timeout, Transport: transport},
		tokens:     map[string]string{},
	}, nil
}

func (c *Client) ListTags(ctx context.Context, repository string) ([]string, error) {
	var tags []string
	repository = cleanRepository(repository)
	nextPath := fmt.Sprintf("/v2/%s/tags/list?n=1000", escapeRepository(repository))

	for nextPath != "" {
		req, err := c.newRequest(ctx, http.MethodGet, nextPath, "")
		if err != nil {
			return nil, err
		}
		resp, err := c.do(req, repository, "pull")
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound && registryErrorCode(body) == "NAME_UNKNOWN" {
				return nil, NameUnknownError{Repository: repository, Status: resp.Status, Body: strings.TrimSpace(string(body))}
			}
			return nil, fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}

		var payload struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			resp.Body.Close()
			return nil, err
		}
		tags = append(tags, payload.Tags...)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		nextPath = nextLinkPath(link)
	}

	return tags, nil
}

func (c *Client) ListRepositories(ctx context.Context) ([]string, error) {
	var repositories []string
	nextPath := "/v2/_catalog?n=1000"

	for nextPath != "" {
		req, err := c.newRequest(ctx, http.MethodGet, nextPath, "")
		if err != nil {
			return nil, err
		}
		resp, err := c.do(req, "_catalog", "catalog")
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}

		var payload struct {
			Repositories []string `json:"repositories"`
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, err
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			resp.Body.Close()
			if errors.Is(err, io.EOF) || len(strings.TrimSpace(string(body))) == 0 {
				return nil, fmt.Errorf("empty response from %s; registry may not allow /v2/_catalog or the configured registry.url may not point to a Docker registry root", nextPath)
			}
			return nil, fmt.Errorf("decode response from %s: %w: %s", nextPath, err, responseSnippet(body))
		}
		repositories = append(repositories, payload.Repositories...)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		nextPath = nextLinkPath(link)
	}

	return repositories, nil
}

func (c *Client) ResolveDigest(ctx context.Context, repository, tag string) (string, error) {
	repository = cleanRepository(repository)
	req, err := c.newRequest(ctx, http.MethodHead, manifestPath(repository, tag), manifestAccept)
	if err != nil {
		return "", err
	}
	resp, err := c.do(req, repository, "pull")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("missing Docker-Content-Digest header")
	}
	return digest, nil
}

func (c *Client) ManifestInfo(ctx context.Context, repository, reference string) (ManifestInfo, error) {
	info, err := c.manifestInfo(ctx, cleanRepository(repository), reference, map[string]bool{})
	if err != nil {
		return ManifestInfo{}, err
	}
	return info, nil
}

func (c *Client) manifestInfo(ctx context.Context, repository, reference string, seen map[string]bool) (ManifestInfo, error) {
	req, err := c.newRequest(ctx, http.MethodGet, manifestPath(repository, reference), manifestAccept)
	if err != nil {
		return ManifestInfo{}, err
	}
	resp, err := c.do(req, repository, "pull")
	if err != nil {
		return ManifestInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ManifestInfo{}, fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ManifestInfo{}, err
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		digest = reference
	}
	if seen[digest] {
		return ManifestInfo{Digest: digest}, nil
	}
	seen[digest] = true

	var probe struct {
		MediaType string `json:"mediaType"`
		Config    *struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
		Manifests []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ManifestInfo{}, fmt.Errorf("decode manifest %s: %w", reference, err)
	}

	info := ManifestInfo{Digest: digest}
	if probe.Config != nil && probe.Config.Digest != "" && probe.Config.Size > 0 {
		info.Blobs = append(info.Blobs, BlobInfo{Digest: probe.Config.Digest, Size: probe.Config.Size})
		created, err := c.configCreated(ctx, repository, probe.Config.Digest)
		if err != nil {
			return ManifestInfo{}, err
		}
		info.Created = created
	}
	for _, layer := range probe.Layers {
		if layer.Digest != "" && layer.Size > 0 {
			info.Blobs = append(info.Blobs, BlobInfo{Digest: layer.Digest, Size: layer.Size})
		}
	}
	for _, child := range probe.Manifests {
		if child.Digest == "" {
			continue
		}
		childInfo, err := c.manifestInfo(ctx, repository, child.Digest, seen)
		if err != nil {
			return ManifestInfo{}, err
		}
		info.Blobs = append(info.Blobs, childInfo.Blobs...)
		info.Created = newestTime(info.Created, childInfo.Created)
	}

	return info, nil
}

func (c *Client) configCreated(ctx context.Context, repository, digest string) (*time.Time, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/v2/%s/blobs/%s", escapeRepository(repository), url.PathEscape(digest)), "")
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req, repository, "pull")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("config blob %s unexpected status %s: %s", digest, resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Created string `json:"created"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode config blob %s: %w", digest, err)
	}
	if payload.Created == "" {
		return nil, nil
	}
	created, err := time.Parse(time.RFC3339Nano, payload.Created)
	if err != nil {
		return nil, nil
	}
	return &created, nil
}

func newestTime(left, right *time.Time) *time.Time {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if right.After(*left) {
		return right
	}
	return left
}

func (c *Client) DeleteManifest(ctx context.Context, repository, digest string) error {
	repository = cleanRepository(repository)
	req, err := c.newRequest(ctx, http.MethodDelete, manifestPath(repository, digest), "")
	if err != nil {
		return err
	}
	resp, err := c.do(req, repository, "delete")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func newTransport(cfg Config) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}

	if cfg.CACertPath != "" {
		pem, err := os.ReadFile(cfg.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA certificate file %q does not contain a PEM certificate", cfg.CACertPath)
		}
		tlsConfig.RootCAs = pool
	}

	transport.TLSClientConfig = tlsConfig
	return transport, nil
}

func (c *Client) newRequest(ctx context.Context, method, apiPath, accept string) (*http.Request, error) {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + apiPath
	u.RawQuery = ""
	if idx := strings.Index(apiPath, "?"); idx >= 0 {
		u.Path = strings.TrimRight(c.baseURL.Path, "/") + apiPath[:idx]
		u.RawQuery = apiPath[idx+1:]
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return req, nil
}

func (c *Client) do(req *http.Request, repository, action string) (*http.Response, error) {
	tokenKey := repository + ":" + action
	if token := c.tokens[tokenKey]; token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	auth, ok := parseBearerChallenge(challenge)
	if !ok {
		return resp, nil
	}
	resp.Body.Close()

	token, err := c.fetchToken(req.Context(), auth, repository, action)
	if err != nil {
		return nil, err
	}
	c.tokens[tokenKey] = token

	retry := req.Clone(req.Context())
	retry.Header = req.Header.Clone()
	retry.Header.Set("Authorization", "Bearer "+token)
	return c.httpClient.Do(retry)
}

func (c *Client) fetchToken(ctx context.Context, auth bearerChallenge, repository, action string) (string, error) {
	u, err := url.Parse(auth.realm)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if auth.service != "" {
		q.Set("service", auth.service)
	}
	if auth.scope != "" {
		q.Set("scope", auth.scope)
	} else {
		q.Set("scope", fmt.Sprintf("repository:%s:%s", repository, action))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", fmt.Errorf("token response did not contain token")
}

type bearerChallenge struct {
	realm   string
	service string
	scope   string
}

func parseBearerChallenge(value string) (bearerChallenge, bool) {
	if !strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return bearerChallenge{}, false
	}
	params := map[string]string{}
	re := regexp.MustCompile(`([a-zA-Z]+)="([^"]*)"`)
	for _, match := range re.FindAllStringSubmatch(value, -1) {
		params[strings.ToLower(match[1])] = match[2]
	}
	if params["realm"] == "" {
		return bearerChallenge{}, false
	}
	return bearerChallenge{
		realm:   params["realm"],
		service: params["service"],
		scope:   params["scope"],
	}, true
}

func manifestPath(repository, reference string) string {
	return fmt.Sprintf("/v2/%s/manifests/%s", escapeRepository(repository), url.PathEscape(reference))
}

func escapeRepository(repository string) string {
	repository = cleanRepository(repository)
	parts := strings.Split(repository, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func cleanRepository(repository string) string {
	return strings.Trim(repository, "/")
}

func nextLinkPath(link string) string {
	if link == "" {
		return ""
	}
	start := strings.Index(link, "<")
	end := strings.Index(link, ">")
	if start < 0 || end <= start {
		return ""
	}
	raw := link[start+1 : end]
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}

func responseSnippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}

func registryErrorCode(body []byte) string {
	var payload struct {
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Errors) == 0 {
		return ""
	}
	return payload.Errors[0].Code
}
