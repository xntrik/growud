package growatt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCache_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	c, err := newCache(dir, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	data := json.RawMessage(`{"plants":[]}`)
	c.set("GET:plant/list?", data)

	got, ok := c.get("GET:plant/list?")
	if !ok {
		t.Fatal("cache miss on recently set entry")
	}
	if string(got) != string(data) {
		t.Errorf("got %s, want %s", got, data)
	}

	if c.hits != 1 {
		t.Errorf("hits = %d, want 1", c.hits)
	}
	if c.miss != 0 {
		t.Errorf("misses = %d, want 0", c.miss)
	}
}

func TestCache_Miss(t *testing.T) {
	dir := t.TempDir()
	c, err := newCache(dir, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	_, ok := c.get("nonexistent")
	if ok {
		t.Error("expected miss for nonexistent key")
	}
	if c.miss != 1 {
		t.Errorf("misses = %d, want 1", c.miss)
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	dir := t.TempDir()
	c, err := newCache(dir, 1*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	data := json.RawMessage(`"test"`)
	c.set("key", data)

	// Wait for TTL to expire
	time.Sleep(5 * time.Millisecond)

	_, ok := c.get("key")
	if ok {
		t.Error("expected miss after TTL expiry")
	}
	if c.miss != 1 {
		t.Errorf("misses = %d, want 1", c.miss)
	}
}

func TestCache_KeyDeterministic(t *testing.T) {
	dir := t.TempDir()
	c, err := newCache(dir, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	k1 := c.key("GET:plant/list?")
	k2 := c.key("GET:plant/list?")
	if k1 != k2 {
		t.Errorf("key not deterministic: %s != %s", k1, k2)
	}

	k3 := c.key("GET:device/list?plant_id=1")
	if k1 == k3 {
		t.Error("different endpoints should produce different keys")
	}
}

func TestCache_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	c, err := newCache(dir, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// Write a corrupt cache file
	path := filepath.Join(dir, c.key("corrupt")+".json")
	os.WriteFile(path, []byte("not json"), 0600)

	_, ok := c.get("corrupt")
	if ok {
		t.Error("expected miss for corrupt cache file")
	}
}
