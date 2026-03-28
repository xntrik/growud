package growatt

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// newTestStore creates a Store backed by a temporary SQLite database.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "test.db"), filepath.Join(dir, "archive"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestNewStore_CreatesSchema(t *testing.T) {
	store := newTestStore(t)

	// Should be able to query the readings table without error
	var count int
	err := store.db.QueryRow("SELECT COUNT(*) FROM readings").Scan(&count)
	if err != nil {
		t.Fatalf("querying readings: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows, got %d", count)
	}
}

func TestUpsertReadings_InsertAndUpdate(t *testing.T) {
	store := newTestStore(t)

	datas := []map[string]any{
		{
			"time":            "2026-03-27 10:00:00",
			"ppv":             float64(1500),
			"soc":             float64(80),
			"plocalLoadTotal": float64(500),
		},
		{
			"time":            "2026-03-27 10:05:00",
			"ppv":             float64(1600),
			"soc":             float64(82),
			"plocalLoadTotal": float64(520),
		},
	}

	inserted, updated, err := store.UpsertReadings("SN001", 5, datas)
	if err != nil {
		t.Fatalf("UpsertReadings: %v", err)
	}
	if inserted != 2 {
		t.Errorf("inserted = %d, want 2", inserted)
	}
	if updated != 0 {
		t.Errorf("updated = %d, want 0", updated)
	}

	// Upsert the same data again — should count as updates
	inserted, updated, err = store.UpsertReadings("SN001", 5, datas)
	if err != nil {
		t.Fatalf("UpsertReadings (second): %v", err)
	}
	if inserted != 0 {
		t.Errorf("second insert: inserted = %d, want 0", inserted)
	}
	if updated != 2 {
		t.Errorf("second insert: updated = %d, want 2", updated)
	}
}

func TestUpsertReadings_SkipsInvalidTime(t *testing.T) {
	store := newTestStore(t)

	datas := []map[string]any{
		{"time": "2026-03-27 10:00:00", "ppv": float64(100)},
		{"ppv": float64(200)},              // no time field
		{"time": "not-a-date", "ppv": float64(300)}, // unparseable
	}

	inserted, _, err := store.UpsertReadings("SN002", 5, datas)
	if err != nil {
		t.Fatalf("UpsertReadings: %v", err)
	}
	if inserted != 1 {
		t.Errorf("inserted = %d, want 1 (invalid times skipped)", inserted)
	}
}

func TestQueryDayReadings(t *testing.T) {
	store := newTestStore(t)

	datas := []map[string]any{
		{
			"time":            "2026-03-27 08:00:00",
			"ppv":             float64(500),
			"plocalLoadTotal": float64(200),
			"soc":             float64(50),
			"pcharge1":        float64(100),
			"pdischarge1":     float64(0),
			"pacToUserTotal":  float64(0),
			"pacToGridTotal":  float64(300),
		},
		{
			"time":            "2026-03-27 12:00:00",
			"ppv":             float64(2000),
			"plocalLoadTotal": float64(600),
			"soc":             float64(90),
		},
		{
			"time": "2026-03-28 08:00:00", // different day
			"ppv":  float64(100),
		},
	}

	_, _, err := store.UpsertReadings("SN003", 5, datas)
	if err != nil {
		t.Fatal(err)
	}

	points, err := store.QueryDayReadings("SN003", "2026-03-27")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2", len(points))
	}

	if points[0].PPVTotal != 500 {
		t.Errorf("first point PPVTotal = %f, want 500", points[0].PPVTotal)
	}
	if points[1].PPVTotal != 2000 {
		t.Errorf("second point PPVTotal = %f, want 2000", points[1].PPVTotal)
	}
}

func TestListDeviceSNs(t *testing.T) {
	store := newTestStore(t)

	// Empty store
	sns, err := store.ListDeviceSNs()
	if err != nil {
		t.Fatal(err)
	}
	if len(sns) != 0 {
		t.Errorf("expected 0 SNs, got %d", len(sns))
	}

	// Add readings for two devices
	data := []map[string]any{{"time": "2026-03-27 10:00:00"}}
	store.UpsertReadings("BBB", 5, data)
	store.UpsertReadings("AAA", 7, data)

	sns, err = store.ListDeviceSNs()
	if err != nil {
		t.Fatal(err)
	}
	if len(sns) != 2 {
		t.Fatalf("expected 2 SNs, got %d", len(sns))
	}
	// Should be sorted
	if sns[0] != "AAA" || sns[1] != "BBB" {
		t.Errorf("got %v, want [AAA BBB]", sns)
	}
}

func TestArchiveDayRaw(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "test.db"), filepath.Join(dir, "archive"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	datas := []map[string]any{{"time": "2026-03-27 10:00:00", "ppv": float64(100)}}
	store.ArchiveDayRaw("SN001", "2026-03-27", datas)

	path := filepath.Join(dir, "archive", "SN001_2026-03-27.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("archive file not created at %s", path)
	}
}

func TestMapGetFloat(t *testing.T) {
	data := map[string]any{
		"valid":   float64(42.5),
		"string":  "not a number",
		"nan":     math.NaN(),
		"inf":     math.Inf(1),
		"neg_inf": math.Inf(-1),
	}

	tests := []struct {
		key  string
		want float64
	}{
		{"valid", 42.5},
		{"missing", 0},
		{"string", 0},
		{"nan", 0},
		{"inf", 0},
		{"neg_inf", 0},
	}
	for _, tt := range tests {
		if got := MapGetFloat(data, tt.key); got != tt.want {
			t.Errorf("MapGetFloat(%q) = %f, want %f", tt.key, got, tt.want)
		}
	}
}

func TestMapGetStr(t *testing.T) {
	data := map[string]any{
		"name":   "hello",
		"number": float64(42),
	}

	if got := MapGetStr(data, "name"); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if got := MapGetStr(data, "missing"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := MapGetStr(data, "number"); got != "" {
		t.Errorf("got %q, want empty (non-string type)", got)
	}
}

func TestTimeSeriesPoint_BatteryPower(t *testing.T) {
	tests := []struct {
		name      string
		charge    float64
		discharge float64
		want      float64
	}{
		{"discharging", 0, 500, 500},
		{"charging", 300, 0, -300},
		{"idle", 0, 0, 0},
		{"discharge takes priority", 100, 200, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := TimeSeriesPoint{ChargePower: tt.charge, DischargePower: tt.discharge}
			if got := p.BatteryPower(); got != tt.want {
				t.Errorf("BatteryPower() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestReadingsSummary(t *testing.T) {
	data := map[string]any{
		"ppv":             float64(1500),
		"soc":             float64(80),
		"plocalLoadTotal": float64(500),
	}
	got := ReadingsSummary("SN001", data)
	want := "SN001: PV 1500W | Battery 80% | Load 500W"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
