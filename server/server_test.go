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
// derived from the cumulative counter deltas where possible, falling back to
// the instantaneous readings clamped to a physical ceiling when the counter
// is flat. This suppresses spurious spikes observed in the Growatt API while
// preserving resolution for small grid flows below the counter's tick.
func TestHandleAPIReadings_DerivedGridPower(t *testing.T) {
	srv := newTestServerWithAPI(t, func(w http.ResponseWriter, r *http.Request) {})

	// Samples 5 minutes apart. Counter quantum is 0.1 kWh, so the ceiling
	// for a 5-min interval is 0.1 * 1000 / (5/60) = 1200 W.
	datas := []map[string]any{
		{
			// Baseline.
			"time":           "2026-03-27 10:00:00",
			"etoUserToday":   float64(0.0),
			"etoGridToday":   float64(1.0),
			"pacToUserTotal": float64(0),
			"pacToGridTotal": float64(2000),
		},
		{
			// Counter ticks; instantaneous has a spurious 6 kW spike. Use
			// the counter delta: 0.1 kWh over 5 min = 1200 W.
			"time":           "2026-03-27 10:05:00",
			"etoUserToday":   float64(0.1),
			"etoGridToday":   float64(1.2),
			"pacToUserTotal": float64(6000),
			"pacToGridTotal": float64(2500),
		},
		{
			// Import counter flat, instantaneous zero → 0 W.
			// Export counter ticks by 0.2 kWh → 2400 W.
			"time":           "2026-03-27 10:10:00",
			"etoUserToday":   float64(0.1),
			"etoGridToday":   float64(1.4),
			"pacToUserTotal": float64(0),
			"pacToGridTotal": float64(2600),
		},
		{
			// Both counters flat; small instantaneous import preserved
			// (below ceiling) and zero export → 0 W.
			"time":           "2026-03-27 10:15:00",
			"etoUserToday":   float64(0.1),
			"etoGridToday":   float64(1.4),
			"pacToUserTotal": float64(500),
			"pacToGridTotal": float64(0),
		},
		{
			// Both counters flat; instantaneous spike clamped to the 1200 W
			// ceiling since sustained >1200 W would have ticked the counter.
			"time":           "2026-03-27 10:20:00",
			"etoUserToday":   float64(0.1),
			"etoGridToday":   float64(1.4),
			"pacToUserTotal": float64(9000),
			"pacToGridTotal": float64(0),
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
	if len(resp.Readings) != 5 {
		t.Fatalf("got %d readings, want 5", len(resp.Readings))
	}

	near := func(got, want, tol float64) bool { return got >= want-tol && got <= want+tol }
	cases := []struct {
		i                    int
		wantIn, wantOut, tol float64
		note                 string
	}{
		{0, 0, 0, 0, "first sample has no prior, left at 0"},
		{1, 1200, 2400, 1, "counter delta path ignores 6 kW spike"},
		{2, 0, 2400, 1, "import flat → 0; export counter ticks"},
		{3, 500, 0, 0, "flat counter + small instantaneous preserved"},
		{4, 1200, 0, 0, "flat counter + big instantaneous clamped to ceiling"},
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
