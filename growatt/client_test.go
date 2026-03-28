package growatt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// newTestServer creates an httptest server that returns canned API responses.
func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func TestNewClient_TrailingSlash(t *testing.T) {
	dir := t.TempDir()

	c1, err := NewClient("http://example.com", "tok", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if c1.baseURL != "http://example.com/" {
		t.Errorf("expected trailing slash, got %q", c1.baseURL)
	}

	c2, err := NewClient("http://example.com/", "tok", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if c2.baseURL != "http://example.com/" {
		t.Errorf("expected trailing slash preserved, got %q", c2.baseURL)
	}
}

func TestNewClient_DefaultTTL(t *testing.T) {
	dir := t.TempDir()
	c, err := NewClient("http://example.com", "tok", dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.cache.ttl != DefaultCacheTTL {
		t.Errorf("expected default TTL %v, got %v", DefaultCacheTTL, c.cache.ttl)
	}
}

func TestClient_TokenHeader(t *testing.T) {
	var gotToken string
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("token")
		json.NewEncoder(w).Encode(map[string]any{
			"data":       map[string]any{},
			"error_code": 0,
			"error_msg":  "",
		})
	})
	defer srv.Close()

	dir := t.TempDir()
	client, err := NewClient(srv.URL, "my-secret-token", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	client.get("test", nil)

	if gotToken != "my-secret-token" {
		t.Errorf("token header = %q, want %q", gotToken, "my-secret-token")
	}
}

func TestClient_APIError(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data":       nil,
			"error_code": 10001,
			"error_msg":  "invalid token",
		})
	})
	defer srv.Close()

	dir := t.TempDir()
	client, err := NewClient(srv.URL, "bad-token", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.get("plant/list", nil)
	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestClient_HTTPError(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})
	defer srv.Close()

	dir := t.TempDir()
	client, err := NewClient(srv.URL, "tok", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.get("plant/list", nil)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestClient_CacheHitMiss(t *testing.T) {
	calls := 0
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{
			"data":       map[string]any{"count": 0, "plants": []any{}},
			"error_code": 0,
			"error_msg":  "",
		})
	})
	defer srv.Close()

	dir := t.TempDir()
	client, err := NewClient(srv.URL, "tok", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// First call — cache miss
	client.get("plant/list", nil)
	if calls != 1 {
		t.Errorf("expected 1 HTTP call, got %d", calls)
	}

	// Second call — should be cached
	client.get("plant/list", nil)
	if calls != 1 {
		t.Errorf("expected still 1 HTTP call (cached), got %d", calls)
	}

	hits, misses := client.CacheStats()
	if hits != 1 || misses != 1 {
		t.Errorf("cache stats: hits=%d misses=%d, want hits=1 misses=1", hits, misses)
	}
}

func TestClient_ResetCacheStats(t *testing.T) {
	dir := t.TempDir()
	client, err := NewClient("http://example.com", "tok", dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	client.cache.hits = 5
	client.cache.miss = 3
	client.ResetCacheStats()

	hits, misses := client.CacheStats()
	if hits != 0 || misses != 0 {
		t.Errorf("after reset: hits=%d misses=%d", hits, misses)
	}
}

func TestCacheKey(t *testing.T) {
	k1 := cacheKey("GET", "plant/list", nil)
	k2 := cacheKey("GET", "plant/list", nil)
	if k1 != k2 {
		t.Error("same input should produce same key")
	}

	k3 := cacheKey("POST", "plant/list", nil)
	if k1 == k3 {
		t.Error("different methods should produce different keys")
	}

	params := url.Values{"plant_id": {"1"}}
	k4 := cacheKey("GET", "plant/data", params)
	k5 := cacheKey("GET", "plant/data", nil)
	if k4 == k5 {
		t.Error("different params should produce different keys")
	}
}
