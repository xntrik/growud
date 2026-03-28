package growatt

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type cacheEntry struct {
	Data      json.RawMessage `json:"data"`
	CachedAt  time.Time       `json:"cached_at"`
	Endpoint  string          `json:"endpoint"`
}

type cache struct {
	dir  string
	ttl  time.Duration
	hits int
	miss int
}

func newCache(dir string, ttl time.Duration) (*cache, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating cache dir: %w", err)
	}
	return &cache{dir: dir, ttl: ttl}, nil
}

func (c *cache) key(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return fmt.Sprintf("%x", h[:8])
}

func (c *cache) get(endpoint string) (json.RawMessage, bool) {
	path := filepath.Join(c.dir, c.key(endpoint)+".json")

	raw, err := os.ReadFile(path)
	if err != nil {
		c.miss++
		return nil, false
	}

	var entry cacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		c.miss++
		return nil, false
	}

	if time.Since(entry.CachedAt) > c.ttl {
		c.miss++
		return nil, false
	}

	c.hits++
	return entry.Data, true
}

func (c *cache) set(endpoint string, data json.RawMessage) {
	entry := cacheEntry{
		Data:     data,
		CachedAt: time.Now(),
		Endpoint: endpoint,
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}

	path := filepath.Join(c.dir, c.key(endpoint)+".json")
	_ = os.WriteFile(path, raw, 0600)
}
