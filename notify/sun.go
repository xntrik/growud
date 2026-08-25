package notify

import (
	"math"
	"time"
)

// Sun position and rise/set times, using the NOAA solar calculator equations.
// Accurate to roughly a minute for latitudes below ~72 degrees, which is well
// inside what a "how much daylight is left" rule needs.

// sunriseZenith is the solar zenith angle at sunrise/sunset. The extra 0.833
// degrees covers atmospheric refraction plus the radius of the solar disc.
const sunriseZenith = 90.833

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// julianDay converts an instant to a Julian day number.
func julianDay(t time.Time) float64 {
	return float64(t.UTC().UnixNano())/8.64e13 + 2440587.5
}

// solarParams holds the intermediate NOAA values shared by the position and
// rise/set calculations.
type solarParams struct {
	declination float64 // degrees
	eqOfTime    float64 // minutes
}

func computeSolarParams(jd float64) solarParams {
	jc := (jd - 2451545.0) / 36525.0

	meanLong := math.Mod(280.46646+jc*(36000.76983+jc*0.0003032), 360)
	if meanLong < 0 {
		meanLong += 360
	}
	meanAnom := 357.52911 + jc*(35999.05029-0.0001537*jc)
	eccent := 0.016708634 - jc*(0.000042037+0.0000001267*jc)

	eqOfCentre := math.Sin(rad(meanAnom))*(1.914602-jc*(0.004817+0.000014*jc)) +
		math.Sin(rad(2*meanAnom))*(0.019993-0.000101*jc) +
		math.Sin(rad(3*meanAnom))*0.000289

	trueLong := meanLong + eqOfCentre
	appLong := trueLong - 0.00569 - 0.00478*math.Sin(rad(125.04-1934.136*jc))

	meanObliq := 23 + (26+(21.448-jc*(46.815+jc*(0.00059-jc*0.001813)))/60)/60
	obliqCorr := meanObliq + 0.00256*math.Cos(rad(125.04-1934.136*jc))

	declination := deg(math.Asin(clamp(math.Sin(rad(obliqCorr))*math.Sin(rad(appLong)), -1, 1)))

	varY := math.Tan(rad(obliqCorr/2)) * math.Tan(rad(obliqCorr/2))
	eqOfTime := 4 * deg(varY*math.Sin(2*rad(meanLong))-
		2*eccent*math.Sin(rad(meanAnom))+
		4*eccent*varY*math.Sin(rad(meanAnom))*math.Cos(2*rad(meanLong))-
		0.5*varY*varY*math.Sin(4*rad(meanLong))-
		1.25*eccent*eccent*math.Sin(2*rad(meanAnom)))

	return solarParams{declination: declination, eqOfTime: eqOfTime}
}

// DayInfo describes the sun's rise and set for a single local calendar day.
type DayInfo struct {
	Sunrise time.Time
	Sunset  time.Time

	// PolarDay is set when the sun never drops below the horizon that day,
	// PolarNight when it never climbs above it. Both leave Sunrise/Sunset zero.
	PolarDay   bool
	PolarNight bool
}

// SunTimes returns sunrise and sunset for the local calendar day containing t.
// Both are returned in loc.
func SunTimes(t time.Time, lat, lon float64, loc *time.Location) DayInfo {
	y, m, d := t.In(loc).Date()

	// Anchor on the UTC day containing local noon. Solar noon at any longitude
	// lands within about an hour of local noon for any sane timezone, so this
	// picks the right UTC day without needing the zone offset explicitly.
	anchor := time.Date(y, m, d, 12, 0, 0, 0, loc).UTC()
	utcMidnight := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC)

	p := computeSolarParams(julianDay(utcMidnight))

	// Solar noon as a fraction of the UTC day.
	noonFrac := (720 - 4*lon - p.eqOfTime) / 1440

	cosHA := math.Cos(rad(sunriseZenith))/(math.Cos(rad(lat))*math.Cos(rad(p.declination))) -
		math.Tan(rad(lat))*math.Tan(rad(p.declination))

	switch {
	case cosHA < -1:
		return DayInfo{PolarDay: true}
	case cosHA > 1:
		return DayInfo{PolarNight: true}
	}

	hourAngle := deg(math.Acos(cosHA))
	noon := utcMidnight.Add(time.Duration(noonFrac * float64(24*time.Hour)))
	halfDay := time.Duration(hourAngle * 4 * float64(time.Minute))

	return DayInfo{
		Sunrise: noon.Add(-halfDay).In(loc),
		Sunset:  noon.Add(halfDay).In(loc),
	}
}

// Position returns the sun's elevation and azimuth in degrees at instant t.
// Elevation is corrected for atmospheric refraction; azimuth is measured
// clockwise from true north.
func Position(t time.Time, lat, lon float64) (elevation, azimuth float64) {
	utc := t.UTC()
	p := computeSolarParams(julianDay(utc))

	minutes := float64(utc.Hour()*60+utc.Minute()) + float64(utc.Second())/60
	trueSolarTime := math.Mod(minutes+p.eqOfTime+4*lon, 1440)
	if trueSolarTime < 0 {
		trueSolarTime += 1440
	}

	hourAngle := trueSolarTime/4 - 180
	if hourAngle < -180 {
		hourAngle += 360
	}

	cosZenith := math.Sin(rad(lat))*math.Sin(rad(p.declination)) +
		math.Cos(rad(lat))*math.Cos(rad(p.declination))*math.Cos(rad(hourAngle))
	zenith := deg(math.Acos(clamp(cosZenith, -1, 1)))

	raw := 90 - zenith
	return raw + refraction(raw), solarAzimuth(lat, p.declination, hourAngle, zenith)
}

// refraction returns the atmospheric refraction correction in degrees for an
// apparent elevation, following the NOAA approximation.
func refraction(elev float64) float64 {
	if elev > 85 {
		return 0
	}
	te := math.Tan(rad(elev))
	var arcsec float64
	switch {
	case elev > 5:
		arcsec = 58.1/te - 0.07/(te*te*te) + 0.000086/(te*te*te*te*te)
	case elev > -0.575:
		arcsec = 1735 + elev*(-518.2+elev*(103.4+elev*(-12.79+elev*0.711)))
	default:
		arcsec = -20.772 / te
	}
	return arcsec / 3600
}

func solarAzimuth(lat, decl, hourAngle, zenith float64) float64 {
	sinZenith := math.Sin(rad(zenith))
	if sinZenith == 0 {
		return 0
	}
	c := (math.Sin(rad(lat))*math.Cos(rad(zenith)) - math.Sin(rad(decl))) /
		(math.Cos(rad(lat)) * sinZenith)
	a := deg(math.Acos(clamp(c, -1, 1)))
	if hourAngle > 0 {
		return math.Mod(a+180, 360)
	}
	return math.Mod(540-a, 360)
}
