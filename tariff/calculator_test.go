package tariff

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xntrik/growud/growatt"
)

func newTestStore(t *testing.T) *growatt.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := growatt.NewStore(filepath.Join(dir, "test.db"), filepath.Join(dir, "archive"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testConfig() *Config {
	return &Config{
		Timezone: "UTC",
		Currency: "AUD",
		Import: []Window{
			{Name: "peak", CentsPerKWh: 45.0, From: "14:00", To: "20:00"},
			{Name: "off_peak", CentsPerKWh: 18.0, From: "00:00", To: "00:00"},
		},
		Export: []Window{
			{Name: "fit", CentsPerKWh: 5.0, From: "00:00", To: "00:00"},
		},
		loc: time.UTC,
	}
}

func TestCalculate_EmptyData(t *testing.T) {
	store := newTestStore(t)
	cfg := testConfig()
	calc := NewCalculator(cfg, store)

	result, err := calc.Calculate("SN001", "2026-03-27", "2026-03-27")
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	if result.Import.TotalKWh != 0 {
		t.Errorf("import total = %f, want 0", result.Import.TotalKWh)
	}
	if result.Export.TotalKWh != 0 {
		t.Errorf("export total = %f, want 0", result.Export.TotalKWh)
	}
}

func TestCalculate_CumulativeCounters(t *testing.T) {
	store := newTestStore(t)

	// Simulate readings with cumulative counters increasing through the day.
	// 10:00 -> 10:05: import goes from 1.0 to 1.5 kWh (0.5 kWh imported in off_peak)
	// 15:00 -> 15:05: import goes from 3.0 to 3.8 kWh (0.8 kWh imported in peak)
	datas := []map[string]any{
		{
			"time":           "2026-03-27 10:00:00",
			"etoUserToday":   float64(1.0),
			"etoGridToday":   float64(0.0),
			"pacToUserTotal": float64(0),
			"pacToGridTotal": float64(0),
		},
		{
			"time":           "2026-03-27 10:05:00",
			"etoUserToday":   float64(1.5),
			"etoGridToday":   float64(0.0),
			"pacToUserTotal": float64(0),
			"pacToGridTotal": float64(0),
		},
		{
			"time":           "2026-03-27 15:00:00",
			"etoUserToday":   float64(3.0),
			"etoGridToday":   float64(2.0),
			"pacToUserTotal": float64(0),
			"pacToGridTotal": float64(0),
		},
		{
			"time":           "2026-03-27 15:05:00",
			"etoUserToday":   float64(3.8),
			"etoGridToday":   float64(2.5),
			"pacToUserTotal": float64(0),
			"pacToGridTotal": float64(0),
		},
	}

	_, _, err := store.UpsertReadings("SN001", 5, datas)
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	calc := NewCalculator(cfg, store)

	result, err := calc.Calculate("SN001", "2026-03-27", "2026-03-27")
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	// Check import buckets
	var peakKWh, offPeakKWh float64
	for _, w := range result.Import.Windows {
		switch w.Name {
		case "peak":
			peakKWh = w.KWh
		case "off_peak":
			offPeakKWh = w.KWh
		}
	}

	if math.Abs(offPeakKWh-0.5) > 0.01 {
		t.Errorf("off_peak import = %.3f kWh, want 0.5", offPeakKWh)
	}
	if math.Abs(peakKWh-0.8) > 0.01 {
		t.Errorf("peak import = %.3f kWh, want 0.8", peakKWh)
	}

	// Check export
	var fitKWh float64
	for _, w := range result.Export.Windows {
		if w.Name == "fit" {
			fitKWh = w.KWh
		}
	}
	if math.Abs(fitKWh-0.5) > 0.01 {
		t.Errorf("fit export = %.3f kWh, want 0.5", fitKWh)
	}

	// Check cost calculation
	expectedPeakCost := 0.8 * 45.0
	for _, w := range result.Import.Windows {
		if w.Name == "peak" {
			if math.Abs(w.CostCents-expectedPeakCost) > 0.1 {
				t.Errorf("peak cost = %.2f cents, want %.2f", w.CostCents, expectedPeakCost)
			}
		}
	}
}

func TestCalculate_TrapezoidalFallback(t *testing.T) {
	store := newTestStore(t)

	// Cross-day readings: cumulative counters reset, so fallback to trapezoidal.
	// Day 1 23:55 -> Day 2 00:05 (10 minutes = 1/6 hour)
	// Import power: 600W avg -> 0.1 kWh
	datas := []map[string]any{
		{
			"time":           "2026-03-27 23:55:00",
			"etoUserToday":   float64(10.0),
			"etoGridToday":   float64(5.0),
			"pacToUserTotal": float64(600),
			"pacToGridTotal": float64(0),
		},
		{
			"time":           "2026-03-28 00:05:00",
			"etoUserToday":   float64(0.1), // reset
			"etoGridToday":   float64(0.0),
			"pacToUserTotal": float64(600),
			"pacToGridTotal": float64(0),
		},
	}

	_, _, err := store.UpsertReadings("SN001", 5, datas)
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	calc := NewCalculator(cfg, store)

	result, err := calc.Calculate("SN001", "2026-03-27", "2026-03-28")
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	// Should use trapezoidal: (600+600)/2 * (10/60) / 1000 = 0.1 kWh
	if math.Abs(result.Import.TotalKWh-0.1) > 0.01 {
		t.Errorf("import total = %.4f kWh, want ~0.1", result.Import.TotalKWh)
	}
}

func TestCalculate_GapSkipped(t *testing.T) {
	store := newTestStore(t)

	// Gap > 30 minutes should be skipped
	datas := []map[string]any{
		{
			"time":           "2026-03-27 10:00:00",
			"etoUserToday":   float64(1.0),
			"etoGridToday":   float64(0),
			"pacToUserTotal": float64(1000),
			"pacToGridTotal": float64(0),
		},
		{
			"time":           "2026-03-27 12:00:00", // 2 hour gap
			"etoUserToday":   float64(5.0),
			"etoGridToday":   float64(0),
			"pacToUserTotal": float64(1000),
			"pacToGridTotal": float64(0),
		},
	}

	_, _, err := store.UpsertReadings("SN001", 5, datas)
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	calc := NewCalculator(cfg, store)

	result, err := calc.Calculate("SN001", "2026-03-27", "2026-03-27")
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	if result.Import.TotalKWh != 0 {
		t.Errorf("import total = %f, want 0 (gap should be skipped)", result.Import.TotalKWh)
	}
}

func TestQueryRangeReadings(t *testing.T) {
	store := newTestStore(t)

	datas := []map[string]any{
		{
			"time":           "2026-03-27 10:00:00",
			"pacToUserTotal": float64(500),
			"pacToGridTotal": float64(100),
			"etoUserToday":   float64(1.0),
			"etoGridToday":   float64(0.5),
		},
		{
			"time":           "2026-03-28 10:00:00",
			"pacToUserTotal": float64(600),
			"pacToGridTotal": float64(200),
			"etoUserToday":   float64(2.0),
			"etoGridToday":   float64(1.0),
		},
		{
			"time": "2026-03-29 10:00:00", // outside range
		},
	}

	_, _, err := store.UpsertReadings("SN001", 5, datas)
	if err != nil {
		t.Fatal(err)
	}

	points, err := store.QueryRangeReadings("SN001", "2026-03-27", "2026-03-28")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2", len(points))
	}
	if points[0].GridImportPower != 500 {
		t.Errorf("point[0] import power = %f, want 500", points[0].GridImportPower)
	}
	if points[1].GridImportToday != 2.0 {
		t.Errorf("point[1] import today = %f, want 2.0", points[1].GridImportToday)
	}
}

func TestIntervalEnergy_SameDay(t *testing.T) {
	prev := growatt.CostReadingPoint{
		Time:            time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC),
		GridImportToday: 1.0,
		GridExportToday: 0.5,
	}
	curr := growatt.CostReadingPoint{
		Time:            time.Date(2026, 3, 27, 10, 5, 0, 0, time.UTC),
		GridImportToday: 1.3,
		GridExportToday: 0.7,
	}

	importKWh, exportKWh := intervalEnergy(prev, curr, 5.0/60.0)
	if math.Abs(importKWh-0.3) > 0.001 {
		t.Errorf("import = %f, want 0.3", importKWh)
	}
	if math.Abs(exportKWh-0.2) > 0.001 {
		t.Errorf("export = %f, want 0.2", exportKWh)
	}
}

func TestIntervalEnergy_CrossDay(t *testing.T) {
	prev := growatt.CostReadingPoint{
		Time:            time.Date(2026, 3, 27, 23, 55, 0, 0, time.UTC),
		GridImportPower: 1000,
		GridExportPower: 0,
		GridImportToday: 10.0,
	}
	curr := growatt.CostReadingPoint{
		Time:            time.Date(2026, 3, 28, 0, 5, 0, 0, time.UTC),
		GridImportPower: 1000,
		GridExportPower: 0,
		GridImportToday: 0.1, // reset
	}

	hours := 10.0 / 60.0
	importKWh, _ := intervalEnergy(prev, curr, hours)
	// Trapezoidal: (1000+1000)/2 * (10/60) / 1000 = 0.1667
	expected := 1000.0 * hours / 1000.0
	if math.Abs(importKWh-expected) > 0.001 {
		t.Errorf("import = %f, want %f", importKWh, expected)
	}
}

func TestLoadConfig_ExampleFile(t *testing.T) {
	// Test loading the example config file
	examplePath := filepath.Join("..", "tariff.example.json")
	if _, err := os.Stat(examplePath); os.IsNotExist(err) {
		t.Skip("tariff.example.json not found")
	}

	cfg, err := LoadConfig(examplePath)
	if err != nil {
		t.Fatalf("LoadConfig(example): %v", err)
	}
	if cfg.Timezone != "Australia/Sydney" {
		t.Errorf("timezone = %q, want Australia/Sydney", cfg.Timezone)
	}
	if len(cfg.Import) == 0 {
		t.Error("no import windows")
	}
	if len(cfg.Export) == 0 {
		t.Error("no export windows")
	}
}
