package notify

import (
	"math"
	"testing"
	"time"
)

func testConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := LoadConfig(writeConfig(t, `{
	  "timezone": "Australia/Sydney",
	  "location": {"latitude": -33.8688, "longitude": 151.2093},
	  "targets": [{"name": "phone", "type": "ntfy", "url": "https://ntfy.sh/x"}],
	  "rules": []
	}`))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

func TestBuildSnapshot_DeviceMetrics(t *testing.T) {
	cfg := testConfig(t)
	data := map[string]any{
		"soc":             96.0,
		"ppv":             3200.0,
		"pcharge1":        800.0,
		"pdischarge1":     0.0,
		"plocalLoadTotal": 1200.0,
		"etoUserToday":    4.5,
		"vac1":            241.3,
	}

	snap := BuildSnapshot(cfg, "SN123", data, time.Date(2026, 12, 21, 13, 0, 0, 0, cfg.TZ()))

	if snap.DeviceSN != "SN123" {
		t.Errorf("DeviceSN = %q", snap.DeviceSN)
	}
	want := map[string]float64{
		"battery_soc":           96,
		"solar_power":           3200,
		"battery_charge_power":  800,
		"load_power":            1200,
		"grid_import_today_kwh": 4.5,
		"grid_voltage":          241.3,
		// Charging, so net battery power is negative.
		"battery_power": -800,
	}
	for name, w := range want {
		if got := snap.Values[name]; math.Abs(got-w) > 1e-9 {
			t.Errorf("Values[%q] = %v, want %v", name, got, w)
		}
	}

	// Fields absent from the payload read as zero rather than going missing,
	// so a rule on them still evaluates.
	if _, ok := snap.Values["battery_temp"]; !ok {
		t.Error("expected battery_temp to be present even when absent from the payload")
	}
}

func TestBuildSnapshot_SunAndClockMetrics(t *testing.T) {
	cfg := testConfig(t)

	// A summer afternoon in Sydney: sunset is around 20:05 daylight time.
	now := time.Date(2026, 12, 21, 15, 30, 0, 0, cfg.TZ())
	snap := BuildSnapshot(cfg, "SN123", map[string]any{}, now)

	untilSunset := snap.Values["minutes_until_sunset"]
	if untilSunset < 240 || untilSunset > 300 {
		t.Errorf("minutes_until_sunset = %.0f, want roughly 4.5h before a summer sunset", untilSunset)
	}
	if got := snap.Values["hours_until_sunset"]; math.Abs(got-untilSunset/60) > 1e-9 {
		t.Errorf("hours_until_sunset = %v, want minutes/60", got)
	}
	if elev := snap.Values["sun_elevation"]; elev < 20 || elev > 60 {
		t.Errorf("sun_elevation = %.1f, want the sun well up on a summer afternoon", elev)
	}
	if length := snap.Values["day_length_minutes"]; length < 840 || length > 900 {
		t.Errorf("day_length_minutes = %.0f, want ~14h25m", length)
	}

	if got := snap.Values["hour"]; math.Abs(got-15.5) > 1e-9 {
		t.Errorf("hour = %v, want 15.5", got)
	}
	if got := snap.Values["minute_of_day"]; got != 930 {
		t.Errorf("minute_of_day = %v, want 930", got)
	}
	// 2026-12-21 is a Monday.
	if got := snap.Values["weekday"]; got != float64(time.Monday) {
		t.Errorf("weekday = %v, want %v", got, float64(time.Monday))
	}
}

func TestBuildSnapshot_AfterSunsetClampsToZero(t *testing.T) {
	cfg := testConfig(t)

	// Late evening, well after the sun has gone down.
	snap := BuildSnapshot(cfg, "SN123", map[string]any{}, time.Date(2026, 6, 21, 22, 0, 0, 0, cfg.TZ()))

	if got := snap.Values["minutes_until_sunset"]; got != 0 {
		t.Errorf("minutes_until_sunset = %v after sunset, want 0", got)
	}
	if got := snap.Values["sun_elevation"]; got >= 0 {
		t.Errorf("sun_elevation = %v at 10pm, want below the horizon", got)
	}
}

func TestBuildSnapshot_ConvertsToConfiguredTimezone(t *testing.T) {
	cfg := testConfig(t)

	// Pass a UTC instant; the snapshot should report Sydney local time.
	utc := time.Date(2026, 12, 21, 4, 30, 0, 0, time.UTC)
	snap := BuildSnapshot(cfg, "SN123", map[string]any{}, utc)

	if got := snap.At.Hour(); got != 15 {
		t.Errorf("snapshot hour = %d, want 15 (Sydney is UTC+11 in December)", got)
	}
}

func TestMetricNamesAndExists(t *testing.T) {
	names := MetricNames()
	if len(names) != len(deviceMetrics)+len(derivedMetrics) {
		t.Errorf("MetricNames() returned %d names, want %d", len(names), len(deviceMetrics)+len(derivedMetrics))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("MetricNames() is not sorted: %q before %q", names[i-1], names[i])
		}
	}

	for _, name := range names {
		if !MetricExists(name) {
			t.Errorf("MetricExists(%q) = false", name)
		}
	}
	if MetricExists("battery_percent") {
		t.Error("MetricExists(\"battery_percent\") = true, want false")
	}
}
