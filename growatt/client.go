package growatt

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultCacheTTL = 5 * time.Minute

// Client is the Growatt OpenAPI V1 HTTP client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	cache      *cache
}

// NewClient creates a new Growatt API client with file-based caching.
// cacheDir is the directory for cache files. cacheTTL of 0 uses the default (5 min).
func NewClient(baseURL, token, cacheDir string, cacheTTL time.Duration) (*Client, error) {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if cacheTTL == 0 {
		cacheTTL = DefaultCacheTTL
	}
	c, err := newCache(cacheDir, cacheTTL)
	if err != nil {
		return nil, fmt.Errorf("initializing cache: %w", err)
	}
	return &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cache:      c,
	}, nil
}

// cacheKey builds a unique key for a request.
func cacheKey(method, path string, params url.Values) string {
	return method + ":" + path + "?" + params.Encode()
}

// get performs a GET request to the given path with query parameters.
func (c *Client) get(path string, params url.Values) (json.RawMessage, error) {
	key := cacheKey("GET", path, params)
	if data, ok := c.cache.get(key); ok {
		return data, nil
	}

	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	data, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	c.cache.set(key, data)
	return data, nil
}

// post performs a POST request with form-encoded body.
func (c *Client) post(path string, form url.Values) (json.RawMessage, error) {
	key := cacheKey("POST", path, form)
	if data, ok := c.cache.get(key); ok {
		return data, nil
	}

	u := c.baseURL + path

	req, err := http.NewRequest("POST", u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	data, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	c.cache.set(key, data)
	return data, nil
}

func (c *Client) doRequest(req *http.Request) (json.RawMessage, error) {
	req.Header.Set("token", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	// Limit response body to 10 MB to prevent OOM from a malicious upstream.
	const maxResponseSize = 10 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Data      json.RawMessage `json:"data"`
		ErrorCode int             `json:"error_code"`
		ErrorMsg  string          `json:"error_msg"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decoding response: %w (body: %s)", err, string(body))
	}

	if envelope.ErrorCode != 0 {
		return nil, fmt.Errorf("API error %d: %s", envelope.ErrorCode, envelope.ErrorMsg)
	}

	return envelope.Data, nil
}

// postNoCache performs a POST request bypassing the cache.
func (c *Client) postNoCache(path string, form url.Values) (json.RawMessage, error) {
	u := c.baseURL + path

	req, err := http.NewRequest("POST", u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.doRequest(req)
}

// CacheStats returns the number of cache hits and misses.
func (c *Client) CacheStats() (hits, misses int) {
	return c.cache.hits, c.cache.miss
}

// ResetCacheStats resets the hit/miss counters to zero.
func (c *Client) ResetCacheStats() {
	c.cache.hits = 0
	c.cache.miss = 0
}
