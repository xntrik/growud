package tariff

import (
	"fmt"
	"time"

	"github.com/xntrik/growud/growatt"
)

// WindowResult holds the calculated energy and cost for a single tariff window.
type WindowResult struct {
	Name        string  `json:"name"`
	KWh         float64 `json:"kwh"`
	CentsPerKWh float64 `json:"cents_per_kwh"`
	CostCents   float64 `json:"cost_cents"`
}

// DirectionResult holds the aggregate results for import or export.
type DirectionResult struct {
	Windows    []WindowResult `json:"windows"`
	TotalKWh   float64        `json:"total_kwh"`
	TotalCents float64        `json:"total_cents"`
}

// CostResult is the complete cost calculation result.
type CostResult struct {
	DeviceSN string          `json:"device"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	Timezone string          `json:"timezone"`
	Currency string          `json:"currency"`
	Import   DirectionResult `json:"import"`
	Export   DirectionResult `json:"export"`
	NetCents float64         `json:"net_cents"`
}

// Calculator computes grid costs from stored readings and a tariff config.
type Calculator struct {
	cfg   *Config
	store *growatt.Store
}

// NewCalculator creates a new cost calculator.
func NewCalculator(cfg *Config, store *growatt.Store) *Calculator {
	return &Calculator{cfg: cfg, store: store}
}

// Calculate computes grid import cost and export credit for a device over a date range.
// from and to are inclusive date strings in YYYY-MM-DD format.
func (c *Calculator) Calculate(deviceSN, from, to string) (*CostResult, error) {
	points, err := c.store.QueryRangeReadings(deviceSN, from, to)
	if err != nil {
		return nil, fmt.Errorf("querying readings: %w", err)
	}

	if len(points) < 2 {
		return &CostResult{
			DeviceSN: deviceSN,
			From:     from,
			To:       to,
			Timezone: c.cfg.Timezone,
			Currency: c.cfg.Currency,
			Import:   DirectionResult{Windows: []WindowResult{}},
			Export:   DirectionResult{Windows: []WindowResult{}},
		}, nil
	}

	// Accumulate kWh per window name
	importBuckets := make(map[string]float64)
	exportBuckets := make(map[string]float64)

	loc := c.cfg.Location()

	for i := 1; i < len(points); i++ {
		prev := points[i-1]
		curr := points[i]

		dt := curr.Time.Sub(prev.Time)
		if dt <= 0 || dt > 30*time.Minute {
			// Skip gaps larger than 30 minutes or non-positive intervals
			continue
		}

		hours := dt.Hours()

		// Compute energy for this interval using the hybrid approach.
		importKWh, exportKWh := intervalEnergy(prev, curr, hours)

		// Determine tariff window at the midpoint of the interval
		midpoint := prev.Time.Add(dt / 2).In(loc)

		if importKWh > 0 {
			if w := c.cfg.MatchImportWindow(midpoint); w != nil {
				importBuckets[w.Name] += importKWh
			}
		}
		if exportKWh > 0 {
			if w := c.cfg.MatchExportWindow(midpoint); w != nil {
				exportBuckets[w.Name] += exportKWh
			}
		}
	}

	result := &CostResult{
		DeviceSN: deviceSN,
		From:     from,
		To:       to,
		Timezone: c.cfg.Timezone,
		Currency: c.cfg.Currency,
	}

	// Build import results
	result.Import = buildDirectionResult(c.cfg.Import, importBuckets)
	result.Export = buildDirectionResult(c.cfg.Export, exportBuckets)
	result.NetCents = result.Import.TotalCents - result.Export.TotalCents

	return result, nil
}

// intervalEnergy computes import and export kWh for a single interval between
// two consecutive readings, using the hybrid approach:
// - Prefer cumulative daily counter deltas when available and monotonic
// - Fall back to trapezoidal integration of instantaneous power
func intervalEnergy(prev, curr growatt.CostReadingPoint, hours float64) (importKWh, exportKWh float64) {
	// Try cumulative counters first (they're in kWh already).
	// The counters reset at midnight, so we only use them if both are on
	// the same date and the delta is non-negative.
	sameDay := prev.Time.YearDay() == curr.Time.YearDay() && prev.Time.Year() == curr.Time.Year()

	if sameDay {
		importDelta := curr.GridImportToday - prev.GridImportToday
		exportDelta := curr.GridExportToday - prev.GridExportToday

		if importDelta >= 0 && exportDelta >= 0 {
			return importDelta, exportDelta
		}
	}

	// Fallback: trapezoidal integration of instantaneous power (watts -> kWh)
	importKWh = (prev.GridImportPower + curr.GridImportPower) / 2.0 * hours / 1000.0
	exportKWh = (prev.GridExportPower + curr.GridExportPower) / 2.0 * hours / 1000.0
	return importKWh, exportKWh
}

func buildDirectionResult(windows []Window, buckets map[string]float64) DirectionResult {
	dr := DirectionResult{
		Windows: make([]WindowResult, 0, len(windows)),
	}

	for _, w := range windows {
		kwh := buckets[w.Name]
		costCents := kwh * w.CentsPerKWh
		dr.Windows = append(dr.Windows, WindowResult{
			Name:        w.Name,
			KWh:         kwh,
			CentsPerKWh: w.CentsPerKWh,
			CostCents:   costCents,
		})
		dr.TotalKWh += kwh
		dr.TotalCents += costCents
	}

	return dr
}
