package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/xntrik/growud/growatt"
)

// newTestServerWithAPI creates a Server backed by a mock Growatt API and temp SQLite store.
func newTestServerWithAPI(t *testing.T, apiHandler http.HandlerFunc) *Server {
	t.Helper()

	mockAPI := httptest.NewServer(apiHandler)
	t.Cleanup(mockAPI.Close)

	dir := t.TempDir()
	client, err := growatt.NewClient(mockAPI.URL, "test-token", filepath.Join(dir, "cache"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	store, err := growatt.NewStore(filepath.Join(dir, "test.db"), filepath.Join(dir, "archive"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	srv, err := NewServer(client, store, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestHandleDashboard(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": map[string]any{
				"count": 1,
				"plants": []map[string]any{
					{"plant_id": "1", "plant_name": "Test Plant", "city": "Sydney", "country": "AU"},
				},
			},
			"error_code": 0,
			"error_msg":  "",
		}
		json.NewEncoder(w).Encode(resp)
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestHandleDashboard_NotFound(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.handleDashboard(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleAPIReadings_Empty(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest("GET", "/api/readings?date=2026-03-27", nil)
	w := httptest.NewRecorder()
	srv.handleAPIReadings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp readingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Date != "2026-03-27" {
		t.Errorf("date = %q", resp.Date)
	}
}

func TestHandleAPIReadings_WithData(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	// Seed data into the store
	datas := []map[string]any{
		{
			"time":            "2026-03-27 10:00:00",
			"ppv":             float64(1000),
			"plocalLoadTotal": float64(400),
			"soc":             float64(75),
			"pcharge1":        float64(0),
			"pdischarge1":     float64(200),
			"pacToUserTotal":  float64(100),
			"pacToGridTotal":  float64(0),
		},
	}
	_, _, err := srv.store.UpsertReadings("SN001", 5, datas)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/readings?date=2026-03-27&device=SN001", nil)
	w := httptest.NewRecorder()
	srv.handleAPIReadings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}

	var resp readingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Readings) != 1 {
		t.Fatalf("got %d readings, want 1", len(resp.Readings))
	}
	if resp.Readings[0].Solar != 1000 {
		t.Errorf("solar = %f, want 1000", resp.Readings[0].Solar)
	}
	if resp.Device != "SN001" {
		t.Errorf("device = %q", resp.Device)
	}
}

func TestHandleAPISummary(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var resp map[string]any
		switch {
		case r.URL.Path == "/plant/list" || r.URL.Path == "/v1/plant/list":
			resp = map[string]any{
				"data": map[string]any{
					"count": 1,
					"plants": []map[string]any{
						{"plant_id": "1", "plant_name": "Test", "city": "Melb", "country": "AU", "status": "1"},
					},
				},
				"error_code": 0,
				"error_msg":  "",
			}
		default:
			resp = map[string]any{
				"data": map[string]any{
					"count":   0,
					"devices": []any{},
				},
				"error_code": 0,
				"error_msg":  "",
			}
		}
		json.NewEncoder(w).Encode(resp)
	})

	req := httptest.NewRequest("GET", "/api/summary", nil)
	w := httptest.NewRecorder()
	srv.handleAPISummary(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}

	var resp summaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Plant.Name != "Test" {
		t.Errorf("plant name = %q", resp.Plant.Name)
	}
}
