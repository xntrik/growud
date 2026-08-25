package notify

import (
	"math"
	"testing"
	"time"
)

func dayLength(d DayInfo) time.Duration { return d.Sunset.Sub(d.Sunrise) }

// solarNoon is the midpoint between sunrise and sunset.
func solarNoon(d DayInfo) time.Time {
	return d.Sunrise.Add(dayLength(d) / 2)
}

func TestSunTimes_EquatorEquinox(t *testing.T) {
	// On the equinox the equator gets a hair over 12 hours of daylight: the
	// 0.833 degree refraction/disc allowance adds a few minutes at each end.
	day := SunTimes(time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC), 0, 0, time.UTC)

	if day.PolarDay || day.PolarNight {
		t.Fatalf("unexpected polar day/night at the equator: %+v", day)
	}
	if got := dayLength(day); got < 12*time.Hour || got > 12*time.Hour+15*time.Minute {
		t.Errorf("day length = %v, want ~12h05m", got)
	}
	if !day.Sunrise.Before(day.Sunset) {
		t.Errorf("sunrise %v is not before sunset %v", day.Sunrise, day.Sunset)
	}
}

// TestSunTimes_SolarNoonTracksLongitude pins the sign and scale of the
// longitude term: solar noon moves four minutes earlier per degree east.
func TestSunTimes_SolarNoonTracksLongitude(t *testing.T) {
	date := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		lon     float64
		zone    *time.Location
		wantUTC time.Duration // expected solar noon, as time past UTC midnight
	}{
		// 720 - 4*150 = 120 minutes past UTC midnight, plus the equation of time.
		{"east", 150, time.FixedZone("+10", 10*3600), 2 * time.Hour},
		// 720 + 4*150 = 1320 minutes past UTC midnight.
		{"west", -150, time.FixedZone("-10", -10*3600), 22 * time.Hour},
		{"greenwich", 0, time.UTC, 12 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			day := SunTimes(date, 0, tt.lon, tt.zone)
			noon := solarNoon(day).UTC()
			midnight := time.Date(noon.Year(), noon.Month(), noon.Day(), 0, 0, 0, 0, time.UTC)

			got := noon.Sub(midnight)
			// The equation of time is worth about 8 minutes near the equinox.
			if diff := got - tt.wantUTC; diff < -20*time.Minute || diff > 20*time.Minute {
				t.Errorf("solar noon = %v past UTC midnight, want ~%v", got, tt.wantUTC)
			}
		})
	}
}

func TestSunTimes_SolsticeDayLength(t *testing.T) {
	// June solstice: long days in the northern hemisphere, short in the
	// southern. Getting the declination sign wrong swaps these.
	june := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	london := SunTimes(june, 51.5074, -0.1278, time.UTC)
	if got := dayLength(london); got < 16*time.Hour+20*time.Minute || got > 17*time.Hour {
		t.Errorf("London June day length = %v, want ~16h40m", got)
	}

	sydney := SunTimes(june, -33.8688, 151.2093, time.FixedZone("AEST", 10*3600))
	if got := dayLength(sydney); got < 9*time.Hour+40*time.Minute || got > 10*time.Hour+10*time.Minute {
		t.Errorf("Sydney June day length = %v, want ~9h55m", got)
	}

	// And the reverse six months later.
	december := time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC)
	sydneySummer := SunTimes(december, -33.8688, 151.2093, time.FixedZone("AEST", 10*3600))
	if got := dayLength(sydneySummer); got < 14*time.Hour || got > 14*time.Hour+40*time.Minute {
		t.Errorf("Sydney December day length = %v, want ~14h25m", got)
	}
}

func TestSunTimes_Polar(t *testing.T) {
	// Longyearbyen, well inside the Arctic Circle.
	const lat, lon = 78.22, 15.63
	zone := time.FixedZone("+01", 3600)

	summer := SunTimes(time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), lat, lon, zone)
	if !summer.PolarDay {
		t.Errorf("expected polar day in June, got %+v", summer)
	}

	winter := SunTimes(time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC), lat, lon, zone)
	if !winter.PolarNight {
		t.Errorf("expected polar night in December, got %+v", winter)
	}
}

func TestPosition_NoonElevation(t *testing.T) {
	// At solar noon the sun's elevation is 90 - |latitude - declination|.
	tests := []struct {
		name     string
		date     time.Time
		lat, lon float64
		zone     *time.Location
		want     float64
	}{
		// Equinox at the equator: declination ~0, sun effectively overhead.
		{"equator equinox", time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC), 0, 0, time.UTC, 90},
		// Sydney at the June solstice: declination +23.4, so 90 - 57.3.
		{"sydney june", time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), -33.8688, 151.2093, time.FixedZone("AEST", 10*3600), 32.7},
		// Sydney at the December solstice: declination -23.4, so 90 - 10.4.
		{"sydney december", time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC), -33.8688, 151.2093, time.FixedZone("AEST", 10*3600), 79.6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			day := SunTimes(tt.date, tt.lat, tt.lon, tt.zone)
			elevation, _ := Position(solarNoon(day), tt.lat, tt.lon)
			if math.Abs(elevation-tt.want) > 1 {
				t.Errorf("noon elevation = %.2f, want ~%.1f", elevation, tt.want)
			}
		})
	}
}

func TestPosition_BelowHorizonAtNight(t *testing.T) {
	const lat, lon = -33.8688, 151.2093
	zone := time.FixedZone("AEST", 10*3600)

	midnight := time.Date(2026, 6, 21, 0, 0, 0, 0, zone)
	elevation, _ := Position(midnight, lat, lon)
	if elevation >= 0 {
		t.Errorf("midnight elevation = %.2f, want below the horizon", elevation)
	}
}

func TestPosition_AzimuthSweepsEastToWest(t *testing.T) {
	// Morning sun in the east, afternoon sun in the west.
	const lat, lon = -33.8688, 151.2093
	zone := time.FixedZone("AEST", 10*3600)

	_, morning := Position(time.Date(2026, 12, 21, 8, 0, 0, 0, zone), lat, lon)
	_, afternoon := Position(time.Date(2026, 12, 21, 16, 0, 0, 0, zone), lat, lon)

	if morning < 45 || morning > 135 {
		t.Errorf("8am azimuth = %.1f, want roughly east", morning)
	}
	if afternoon < 225 || afternoon > 315 {
		t.Errorf("4pm azimuth = %.1f, want roughly west", afternoon)
	}
}

func TestSunTimes_SunriseElevationIsNearHorizon(t *testing.T) {
	// Cross-check the two calculations against each other: the elevation at
	// the moment SunTimes reports for sunrise should be about zero.
	day := SunTimes(time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), -33.8688, 151.2093, time.FixedZone("AEST", 10*3600))

	elevation, _ := Position(day.Sunrise, -33.8688, 151.2093)
	if math.Abs(elevation) > 0.6 {
		t.Errorf("elevation at sunrise = %.3f, want ~0", elevation)
	}
}

// TestSolarParams pins the two values every other calculation is built on,
// against published figures. Both SunTimes and Position read them, so a drift
// here would move sunrise, sunset and elevation together — which the
// consistency checks above would not catch on their own.
func TestSolarParams(t *testing.T) {
	tests := []struct {
		name            string
		date            time.Time
		wantDeclination float64
		wantEqOfTime    float64 // minutes
	}{
		{"march equinox", time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC), 0, -7.4},
		{"june solstice", time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), 23.44, -1.8},
		{"september equinox", time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC), 0, 7.3},
		{"december solstice", time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC), -23.44, 1.9},
		// The equation of time's annual extremes.
		{"equation of time minimum", time.Date(2026, 2, 11, 12, 0, 0, 0, time.UTC), -13.9, -14.2},
		{"equation of time maximum", time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), -15.2, 16.4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := computeSolarParams(julianDay(tt.date))
			if math.Abs(p.declination-tt.wantDeclination) > 0.3 {
				t.Errorf("declination = %+.3f, want %+.2f", p.declination, tt.wantDeclination)
			}
			if math.Abs(p.eqOfTime-tt.wantEqOfTime) > 0.3 {
				t.Errorf("equation of time = %+.3f min, want %+.1f", p.eqOfTime, tt.wantEqOfTime)
			}
		})
	}
}
