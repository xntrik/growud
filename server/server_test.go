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

// TestHandleAPIReadings_DerivedGridPower verifies that grid_in/grid_out are
// derived from the cumulative counter deltas rather than the raw
// pacToUserTotal / pacToGridTotal fields. This is the fix for spurious spikes
// returned occasionally by the Growatt API in the instantaneous fields.
func TestHandleAPIReadings_DerivedGridPower(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	// Three samples 5 minutes apart. The instantaneous pacToUserTotal has a
	// bogus 6000 W spike in the middle sample; the counter only advances by
	// 0.1 kWh between samples, so the derived value should be sane.
	datas := []map[string]any{
		{
			"time":           "2026-03-27 10:00:00",
			"etoUserToday":   float64(0.0),
			"etoGridToday":   float64(1.0),
			"pacToUserTotal": float64(0),
			"pacToGridTotal": float64(2000),
		},
		{
			"time":           "2026-03-27 10:05:00",
			"etoUserToday":   float64(0.1), // 0.1 kWh imported in 5 min = 1200 W avg
			"etoGridToday":   float64(1.2), // 0.2 kWh exported in 5 min = 2400 W avg
			"pacToUserTotal": float64(6000), // spurious spike
			"pacToGridTotal": float64(2500),
		},
		{
			"time":           "2026-03-27 10:10:00",
			"etoUserToday":   float64(0.1), // no further import
			"etoGridToday":   float64(1.4), // another 0.2 kWh export
			"pacToUserTotal": float64(0),
			"pacToGridTotal": float64(2600),
		},
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
	if len(resp.Readings) != 3 {
		t.Fatalf("got %d readings, want 3", len(resp.Readings))
	}

	// First sample: no prior sample, derived values are 0.
	if resp.Readings[0].GridIn != 0 || resp.Readings[0].GridOut != 0 {
		t.Errorf("sample 0: GridIn=%f, GridOut=%f, want 0,0",
			resp.Readings[0].GridIn, resp.Readings[0].GridOut)
	}

	// Second sample: 0.1 kWh over 5 min = 1200 W. The 6000 W spike is ignored.
	if got := resp.Readings[1].GridIn; got < 1199 || got > 1201 {
		t.Errorf("sample 1: GridIn=%f, want ~1200 (not the 6000 W spike)", got)
	}
	if got := resp.Readings[1].GridOut; got < 2399 || got > 2401 {
		t.Errorf("sample 1: GridOut=%f, want ~2400", got)
	}

	// Third sample: no further import, counter flat → 0 W.
	if resp.Readings[2].GridIn != 0 {
		t.Errorf("sample 2: GridIn=%f, want 0", resp.Readings[2].GridIn)
	}
	if got := resp.Readings[2].GridOut; got < 2399 || got > 2401 {
		t.Errorf("sample 2: GridOut=%f, want ~2400", got)
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
