package server

import (
	"encoding/json"
	"fmt"
	"math"
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

	// Night-time samples 5 minutes apart at a steady ~7 kW load fed from
	// the grid, mirroring real data. The counter advances 0.5 or 0.6 kWh
	// per sample because of its 0.1 kWh resolution; the chart must not
	// turn that into a 6.0/7.2 kW sawtooth.
	datas := []map[string]any{
		{"time": "2026-03-27 01:00:00", "pacToUserTotal": float64(6980), "plocalLoadTotal": float64(6980), "etoUserToday": float64(0.2), "etoGridToday": float64(0)},
		{"time": "2026-03-27 01:05:00", "pacToUserTotal": float64(6980), "plocalLoadTotal": float64(6980), "etoUserToday": float64(0.8), "etoGridToday": float64(0)},
		{"time": "2026-03-27 01:10:00", "pacToUserTotal": float64(6970), "plocalLoadTotal": float64(6970), "etoUserToday": float64(1.4), "etoGridToday": float64(0)},
		{"time": "2026-03-27 01:15:00", "pacToUserTotal": float64(7020), "plocalLoadTotal": float64(7020), "etoUserToday": float64(2.0), "etoGridToday": float64(0)},
		{"time": "2026-03-27 01:20:00", "pacToUserTotal": float64(7070), "plocalLoadTotal": float64(7070), "etoUserToday": float64(2.5), "etoGridToday": float64(0)},
		{"time": "2026-03-27 01:25:00", "pacToUserTotal": float64(7040), "plocalLoadTotal": float64(7040), "etoUserToday": float64(3.1), "etoGridToday": float64(0)},
		{"time": "2026-03-27 01:30:00", "pacToUserTotal": float64(6980), "plocalLoadTotal": float64(6980), "etoUserToday": float64(3.7), "etoGridToday": float64(0)},
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
	if len(resp.Readings) != len(datas) {
		t.Fatalf("got %d readings, want %d", len(resp.Readings), len(datas))
	}
	for i, r := range resp.Readings {
		want := datas[i]["plocalLoadTotal"].(float64)
		if r.GridIn < want-1 || r.GridIn > want+1 {
			t.Errorf("sample %d: GridIn=%f, want %f (flat plateau, no sawtooth)", i, r.GridIn, want)
		}
		if r.GridOut != 0 {
			t.Errorf("sample %d: GridOut=%f, want 0", i, r.GridOut)
		}
	}
}

func gridPoint(hhmm string, pv, load, charge, discharge, impToday, expToday float64) growatt.TimeSeriesPoint {
	tm, err := time.Parse("2006-01-02 15:04", "2026-03-27 "+hhmm)
	if err != nil {
		panic(err)
	}
	return growatt.TimeSeriesPoint{
		Time: tm, PPVTotal: pv, LoadPower: load, ChargePower: charge, DischargePower: discharge,
		GridImportToday: impToday, GridExportToday: expToday,
	}
}

func integrateKWh(points []growatt.TimeSeriesPoint, watts []float64) float64 {
	times := make([]time.Time, len(points))
	for i, p := range points {
		times[i] = p.Time
	}
	return trapezoidKWh(times, watts)
}

func TestDeriveGridPower_BalanceBeatsZeroedAPIField(t *testing.T) {
	// The API's pacToUserTotal reads 0 while the battery is capped at
	// 5 kW and the load is above it. The energy balance still shows the
	// import, and the counter confirms it. Import over the window should
	// follow load - discharge, not zero.
	var pts []growatt.TimeSeriesPoint
	for i := 0; i < 8; i++ {
		// 1 kW of real import = 0.0833 kWh per 5-min sample; the 0.1 kWh
		// counter lags behind and catches up, as the device's does.
		counter := 2.0 + math.Floor(float64(i)*5.0/60.0*10+1e-9)/10
		pts = append(pts, gridPoint(fmt.Sprintf("19:%02d", i*5), 0, 6000, 0, 5000, counter, 2.6))
	}
	gridIn, gridOut := deriveGridPower(pts)
	for i, v := range gridIn {
		if v < 900 || v > 1100 {
			t.Errorf("sample %d: GridIn=%f, want ~1000 from balance", i, v)
		}
		if gridOut[i] != 0 {
			t.Errorf("sample %d: GridOut=%f, want 0", i, gridOut[i])
		}
	}
}

func TestDeriveGridPower_PhantomFlowSuppressedByFlatCounter(t *testing.T) {
	// Battery charging at 5 kW from PV for two hours; inverter losses make
	// the balance show ~300 W of "import" that the counter never records.
	// Over 2 h that is 0.6 kWh, so it must be scaled down to at most one
	// quantum (0.1 kWh).
	var pts []growatt.TimeSeriesPoint
	for i := 0; i < 25; i++ {
		pts = append(pts, gridPoint(fmt.Sprintf("%02d:%02d", 10+i/12, (i%12)*5), 5000, 800, 4500, 0, 1.0, 0.1))
	}
	gridIn, _ := deriveGridPower(pts)
	if kwh := integrateKWh(pts, gridIn); kwh > counterQuantumKWh+1e-9 {
		t.Errorf("phantom import integrates to %.3f kWh, want <= %.1f", kwh, counterQuantumKWh)
	}
	for i, v := range gridIn {
		if v < 0 || v > 300 {
			t.Errorf("sample %d: GridIn=%f, want scaled-down phantom in [0,300]", i, v)
		}
	}
}

func TestDeriveGridPower_MissedBurstsPulledUpToCounter(t *testing.T) {
	// Cloudy day: export happens in bursts between samples. The sampled
	// balance only sees a little export, but the counter moved 0.8 kWh in
	// 30 minutes. The series must be scaled up so its integral lands
	// within one quantum of the counter.
	pts := []growatt.TimeSeriesPoint{
		gridPoint("13:50", 2770, 750, 1150, 0, 1.1, 1.8),
		gridPoint("13:55", 6280, 3160, 1150, 0, 1.1, 1.9),
		gridPoint("14:00", 5350, 3100, 1150, 0, 1.1, 2.0),
		gridPoint("14:05", 5730, 3090, 680, 0, 1.1, 2.1),
		gridPoint("14:10", 3020, 3070, 0, 240, 1.1, 2.2),
		gridPoint("14:15", 1940, 2890, 0, 950, 1.1, 2.3),
		gridPoint("14:20", 1560, 2860, 0, 1300, 1.1, 2.6),
	}
	_, gridOut := deriveGridPower(pts)
	shape := make([]float64, len(pts))
	for i, p := range pts {
		if net := p.PPVTotal + p.DischargePower - p.ChargePower - p.LoadPower; net > 0 {
			shape[i] = net
		}
	}
	raw := integrateKWh(pts, shape)
	got := integrateKWh(pts, gridOut)
	want := 0.8
	if got <= raw {
		t.Fatalf("reconciled export %.3f kWh should exceed raw %.3f kWh", got, raw)
	}
	if got < want-counterQuantumKWh-0.02 || got > want+counterQuantumKWh+0.02 {
		t.Errorf("reconciled export = %.3f kWh, want within one quantum of %.1f", got, want)
	}
}

func TestDeriveGridPower_FlatFillWhenShapeIsZero(t *testing.T) {
	// Every instantaneous channel reads zero but the counter moved by more
	// than one quantum: spread the energy flat rather than drop it.
	pts := []growatt.TimeSeriesPoint{
		gridPoint("02:00", 0, 0, 0, 0, 1.0, 0),
		gridPoint("02:15", 0, 0, 0, 0, 1.0, 0),
		gridPoint("02:30", 0, 0, 0, 0, 1.3, 0),
	}
	gridIn, _ := deriveGridPower(pts)
	// 0.3 kWh over 30 min = 600 W.
	for i, v := range gridIn {
		if v < 599 || v > 601 {
			t.Errorf("sample %d: GridIn=%f, want 600 flat fill", i, v)
		}
	}

	// A single-quantum tick with no shape is within tolerance of zero and
	// is not invented.
	pts[2].GridImportToday = 1.1
	gridIn, _ = deriveGridPower(pts)
	for i, v := range gridIn {
		if v != 0 {
			t.Errorf("sample %d: GridIn=%f, want 0 for a lone tick", i, v)
		}
	}
}

func TestDeriveGridPower_Degenerate(t *testing.T) {
	if in, out := deriveGridPower(nil); len(in) != 0 || len(out) != 0 {
		t.Errorf("nil input: got %v %v", in, out)
	}
	one := []growatt.TimeSeriesPoint{gridPoint("12:00", 1000, 3000, 0, 0, 5, 1)}
	in, out := deriveGridPower(one)
	if len(in) != 1 || in[0] != 2000 || out[0] != 0 {
		t.Errorf("single point: got in=%v out=%v, want in=[2000] out=[0]", in, out)
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
