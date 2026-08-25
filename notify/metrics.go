package notify

import (
	"math"
	"sort"
	"time"

	"github.com/xntrik/growud/growatt"
)

// deviceMetrics maps the metric names used in rules onto the raw Growatt API
// field names. These are the same fields the store and dashboard read.
var deviceMetrics = map[string]string{
	"battery_soc":             "soc",
	"battery_charge_power":    "pcharge1",
	"battery_discharge_power": "pdischarge1",
	"battery_voltage":         "vbat",
	"battery_temp":            "batteryTemperature",
	"battery_soh":             "bmsSOH",
	"solar_power":             "ppv",
	"solar_power_pv1":         "ppv1",
	"solar_power_pv2":         "ppv2",
	"solar_today_kwh":         "epvtoday",
	"load_power":              "plocalLoadTotal",
	"load_today_kwh":          "elocalLoadToday",
	"self_use_today_kwh":      "eselftoday",
	"grid_import_power":       "pacToUserTotal",
	"grid_export_power":       "pacToGridTotal",
	"grid_import_today_kwh":   "etoUserToday",
	"grid_export_today_kwh":   "etoGridToday",
	"grid_voltage":            "vac1",
	"grid_frequency":          "fac",
	"inverter_temp":           "temp1",
}

// derivedMetrics are computed from the device data, the clock, or the sun's
// position rather than read straight off the API payload.
var derivedMetrics = []string{
	"battery_power",
	"sun_elevation",
	"sun_azimuth",
	"minutes_until_sunset",
	"minutes_since_sunrise",
	"hours_until_sunset",
	"day_length_minutes",
	"hour",
	"minute_of_day",
	"weekday",
}

// Snapshot is one evaluation's view of the system: every metric a rule can
// test, plus the context a message template can interpolate.
type Snapshot struct {
	DeviceSN string
	At       time.Time // local time in the configured timezone
	Sunrise  time.Time
	Sunset   time.Time
	Values   map[string]float64
}

// BuildSnapshot assembles the metric set from a device's latest data payload.
func BuildSnapshot(cfg *Config, deviceSN string, data map[string]any, now time.Time) Snapshot {
	loc := cfg.TZ()
	local := now.In(loc)

	vals := make(map[string]float64, len(deviceMetrics)+len(derivedMetrics))
	for name, field := range deviceMetrics {
		vals[name] = growatt.MapGetFloat(data, field)
	}

	// Net battery power: positive discharging, negative charging.
	vals["battery_power"] = vals["battery_discharge_power"] - vals["battery_charge_power"]

	elevation, azimuth := Position(now, cfg.Site.Latitude, cfg.Site.Longitude)
	vals["sun_elevation"] = elevation
	vals["sun_azimuth"] = azimuth

	day := SunTimes(now, cfg.Site.Latitude, cfg.Site.Longitude, loc)
	switch {
	case day.PolarDay:
		vals["minutes_until_sunset"] = 1440
		vals["minutes_since_sunrise"] = 1440
		vals["day_length_minutes"] = 1440
	case day.PolarNight:
		vals["minutes_until_sunset"] = 0
		vals["minutes_since_sunrise"] = 0
		vals["day_length_minutes"] = 0
	default:
		vals["minutes_until_sunset"] = math.Max(0, day.Sunset.Sub(local).Minutes())
		vals["minutes_since_sunrise"] = math.Max(0, local.Sub(day.Sunrise).Minutes())
		vals["day_length_minutes"] = day.Sunset.Sub(day.Sunrise).Minutes()
	}
	vals["hours_until_sunset"] = vals["minutes_until_sunset"] / 60

	vals["hour"] = float64(local.Hour()) + float64(local.Minute())/60
	vals["minute_of_day"] = float64(local.Hour()*60 + local.Minute())
	vals["weekday"] = float64(local.Weekday())

	return Snapshot{
		DeviceSN: deviceSN,
		At:       local,
		Sunrise:  day.Sunrise,
		Sunset:   day.Sunset,
		Values:   vals,
	}
}

// MetricExists reports whether name is a metric the engine produces.
func MetricExists(name string) bool {
	if _, ok := deviceMetrics[name]; ok {
		return true
	}
	for _, d := range derivedMetrics {
		if d == name {
			return true
		}
	}
	return false
}

// MetricNames returns every available metric name, sorted.
func MetricNames() []string {
	names := make([]string, 0, len(deviceMetrics)+len(derivedMetrics))
	for name := range deviceMetrics {
		names = append(names, name)
	}
	names = append(names, derivedMetrics...)
	sort.Strings(names)
	return names
}
