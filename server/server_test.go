package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestHandleDashboard_MethodNotAllowed(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()
	srv.handleDashboard(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleAPISummary_MethodNotAllowed(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest("POST", "/api/summary", nil)
	w := httptest.NewRecorder()
	srv.handleAPISummary(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleAPIReadings_MethodNotAllowed(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest("DELETE", "/api/readings", nil)
	w := httptest.NewRecorder()
	srv.handleAPIReadings(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleAPIReadings_InvalidDate(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	tests := []struct {
		name string
		date string
	}{
		{"bad format", "not-a-date"},
		{"SQL injection", "2026-01-01' OR 1=1--"},
		{"partial date", "2026-03"},
		{"invalid calendar date", "2026-13-45"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := "/api/readings?date=" + url.QueryEscape(tt.date)
			req := httptest.NewRequest("GET", u, nil)
			w := httptest.NewRecorder()
			srv.handleAPIReadings(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for date %q", w.Code, tt.date)
			}
		})
	}
}

func TestHandleAPIReadings_InvalidDevice(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	tests := []struct {
		name   string
		device string
	}{
		{"spaces", "SN 001"},
		{"SQL injection", "SN001'; DROP TABLE readings;--"},
		{"special chars", "SN<script>alert(1)</script>"},
		{"too long", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := "/api/readings?date=2026-03-27&device=" + url.QueryEscape(tt.device)
			req := httptest.NewRequest("GET", u, nil)
			w := httptest.NewRecorder()
			srv.handleAPIReadings(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for device %q", w.Code, tt.device)
			}
		})
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

// TestHandleAPIReadings_DerivedGridPower verifies counter-delta spreading:
// energy is distributed evenly across all samples between consecutive counter
// ticks, producing a smooth curve instead of a "comb" of spikes. Trailing
// samples after the last tick use clamped instantaneous values.
func TestHandleAPIReadings_DerivedGridPower(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	// Overnight import at ~200 W. Counter ticks by 0.1 kWh after 30 min
	// (6 intervals × 5 min). Without spreading, sample 6 would spike to
	// 1200 W with zeros before it. With spreading, all 7 samples get ~200 W.
	//
	// Sample 3 has a spurious 6 kW spike in pacToUserTotal — the spreading
	// absorbs it since the counter hasn't ticked yet.
	//
	// After the tick (samples 7-8), clamped instantaneous is used:
	// sample 7 (300 W, below 1200 W ceiling) passes through; sample 8
	// (8 kW spike) is clamped to the 1200 W ceiling.
	datas := []map[string]any{
		{"time": "2026-03-27 00:00:00", "etoUserToday": float64(0.0), "etoGridToday": float64(0.0), "pacToUserTotal": float64(200), "pacToGridTotal": float64(0)},
		{"time": "2026-03-27 00:05:00", "etoUserToday": float64(0.0), "etoGridToday": float64(0.0), "pacToUserTotal": float64(200), "pacToGridTotal": float64(0)},
		{"time": "2026-03-27 00:10:00", "etoUserToday": float64(0.0), "etoGridToday": float64(0.0), "pacToUserTotal": float64(200), "pacToGridTotal": float64(0)},
		{"time": "2026-03-27 00:15:00", "etoUserToday": float64(0.0), "etoGridToday": float64(0.0), "pacToUserTotal": float64(6000), "pacToGridTotal": float64(0)},
		{"time": "2026-03-27 00:20:00", "etoUserToday": float64(0.0), "etoGridToday": float64(0.0), "pacToUserTotal": float64(200), "pacToGridTotal": float64(0)},
		{"time": "2026-03-27 00:25:00", "etoUserToday": float64(0.0), "etoGridToday": float64(0.0), "pacToUserTotal": float64(200), "pacToGridTotal": float64(0)},
		{"time": "2026-03-27 00:30:00", "etoUserToday": float64(0.1), "etoGridToday": float64(0.0), "pacToUserTotal": float64(200), "pacToGridTotal": float64(0)},
		{"time": "2026-03-27 00:35:00", "etoUserToday": float64(0.1), "etoGridToday": float64(0.0), "pacToUserTotal": float64(300), "pacToGridTotal": float64(0)},
		{"time": "2026-03-27 00:40:00", "etoUserToday": float64(0.1), "etoGridToday": float64(0.0), "pacToUserTotal": float64(8000), "pacToGridTotal": float64(0)},
	}
	if _, _, err := srv.store.UpsertReadings("SN001", 5, datas); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/readings?date=2026-03-27&device=SN001", nil)
	w := httptest.NewRecorder()
	srv.handleAPIReadings(w, req)

	var resp readingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Readings) != 9 {
		t.Fatalf("got %d readings, want 9", len(resp.Readings))
	}

	near := func(got, want, tol float64) bool { return got >= want-tol && got <= want+tol }
	cases := []struct {
		i                    int
		wantIn, wantOut, tol float64
		note                 string
	}{
		{0, 200, 0, 1, "spread: start of span"},
		{1, 200, 0, 1, "spread: mid span"},
		{2, 200, 0, 1, "spread: mid span"},
		{3, 200, 0, 1, "spread: 6 kW spike absorbed by counter span"},
		{4, 200, 0, 1, "spread: mid span"},
		{5, 200, 0, 1, "spread: mid span"},
		{6, 200, 0, 1, "spread: counter tick, end of span"},
		{7, 300, 0, 0, "trailing: 300 W below ceiling, preserved"},
		{8, 1200, 0, 0, "trailing: 8 kW spike clamped to 1200 W ceiling"},
	}
	for _, c := range cases {
		r := resp.Readings[c.i]
		if !near(r.GridIn, c.wantIn, c.tol) {
			t.Errorf("sample %d (%s): GridIn=%f, want %f", c.i, c.note, r.GridIn, c.wantIn)
		}
		if !near(r.GridOut, c.wantOut, c.tol) {
			t.Errorf("sample %d (%s): GridOut=%f, want %f", c.i, c.note, r.GridOut, c.wantOut)
		}
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
