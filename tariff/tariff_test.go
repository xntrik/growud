package tariff

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tariff.json")
	os.WriteFile(path, []byte(`{
		"timezone": "UTC",
		"currency": "AUD",
		"import": [
			{"name": "peak", "cents_per_kwh": 45.0, "from": "14:00", "to": "20:00", "days": ["mon","tue","wed","thu","fri"]},
			{"name": "off_peak", "cents_per_kwh": 18.0, "from": "00:00", "to": "00:00"}
		],
		"export": [
			{"name": "fit", "cents_per_kwh": 5.0, "from": "00:00", "to": "00:00"}
		]
	}`), 0644)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("timezone = %q, want UTC", cfg.Timezone)
	}
	if len(cfg.Import) != 2 {
		t.Errorf("import windows = %d, want 2", len(cfg.Import))
	}
	if len(cfg.Export) != 1 {
		t.Errorf("export windows = %d, want 1", len(cfg.Export))
	}
	if cfg.Location() == nil {
		t.Error("Location() returned nil")
	}
}

func TestLoadConfig_MissingTimezone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tariff.json")
	os.WriteFile(path, []byte(`{"import": [], "export": []}`), 0644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing timezone")
	}
}

func TestLoadConfig_InvalidWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tariff.json")
	os.WriteFile(path, []byte(`{
		"timezone": "UTC",
		"import": [{"name": "bad", "cents_per_kwh": 10, "from": "25:00", "to": "06:00"}],
		"export": []
	}`), 0644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid time")
	}
}

func TestWindowMatches_NormalWindow(t *testing.T) {
	w := &Window{Name: "peak", From: "14:00", To: "20:00"}

	tests := []struct {
		hour, min int
		want      bool
	}{
		{13, 59, false},
		{14, 0, true},
		{17, 30, true},
		{19, 59, true},
		{20, 0, false},
	}
	for _, tt := range tests {
		lt := time.Date(2026, 3, 27, tt.hour, tt.min, 0, 0, time.UTC) // Friday
		if got := windowMatches(w, lt); got != tt.want {
			t.Errorf("%02d:%02d: got %v, want %v", tt.hour, tt.min, got, tt.want)
		}
	}
}

func TestWindowMatches_OvernightWindow(t *testing.T) {
	w := &Window{Name: "off_peak", From: "22:00", To: "07:00"}

	tests := []struct {
		hour int
		want bool
	}{
		{21, false},
		{22, true},
		{23, true},
		{0, true},
		{3, true},
		{6, true},
		{7, false},
		{12, false},
	}
	for _, tt := range tests {
		lt := time.Date(2026, 3, 27, tt.hour, 0, 0, 0, time.UTC)
		if got := windowMatches(w, lt); got != tt.want {
			t.Errorf("%02d:00: got %v, want %v", tt.hour, got, tt.want)
		}
	}
}

func TestWindowMatches_AllDay(t *testing.T) {
	w := &Window{Name: "flat", From: "00:00", To: "00:00"}

	for hour := 0; hour < 24; hour++ {
		lt := time.Date(2026, 3, 27, hour, 0, 0, 0, time.UTC)
		if !windowMatches(w, lt) {
			t.Errorf("%02d:00: expected match for all-day window", hour)
		}
	}
}

func TestDayMatches(t *testing.T) {
	tests := []struct {
		days []string
		wd   time.Weekday
		want bool
	}{
		{nil, time.Monday, true},                        // empty = all
		{[]string{"all"}, time.Saturday, true},          // explicit all
		{[]string{"mon", "tue"}, time.Monday, true},     // match
		{[]string{"mon", "tue"}, time.Wednesday, false}, // no match
		{[]string{"sat", "sun"}, time.Sunday, true},     // weekend
		{[]string{"sat", "sun"}, time.Friday, false},    // weekday
	}
	for _, tt := range tests {
		if got := dayMatches(tt.days, tt.wd); got != tt.want {
			t.Errorf("dayMatches(%v, %v) = %v, want %v", tt.days, tt.wd, got, tt.want)
		}
	}
}

func TestMatchImportWindow(t *testing.T) {
	cfg := &Config{
		Timezone: "UTC",
		Import: []Window{
			{Name: "peak", CentsPerKWh: 45, From: "14:00", To: "20:00", Days: []string{"mon", "tue", "wed", "thu", "fri"}},
			{Name: "off_peak", CentsPerKWh: 18, From: "00:00", To: "00:00"}, // catch-all
		},
	}
	cfg.loc = time.UTC

	// Weekday afternoon -> peak
	fri := time.Date(2026, 3, 27, 15, 0, 0, 0, time.UTC) // Friday
	w := cfg.MatchImportWindow(fri)
	if w == nil || w.Name != "peak" {
		t.Errorf("Friday 15:00: got %v, want peak", w)
	}

	// Saturday -> off_peak (catch-all, since peak only applies to weekdays)
	sat := time.Date(2026, 3, 28, 15, 0, 0, 0, time.UTC) // Saturday
	w = cfg.MatchImportWindow(sat)
	if w == nil || w.Name != "off_peak" {
		t.Errorf("Saturday 15:00: got %v, want off_peak", w)
	}
}
