package growatt

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const createTableSQL = `
CREATE TABLE IF NOT EXISTS readings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    recorded_at TIMESTAMP NOT NULL,
    device_sn TEXT NOT NULL,
    device_type INTEGER NOT NULL,
    status_text TEXT,

    -- Solar
    ppv_total REAL,
    ppv1 REAL,
    ppv2 REAL,
    vpv1 REAL,
    vpv2 REAL,
    epv_today REAL,
    epv_total REAL,

    -- Battery
    soc REAL,
    charge_power REAL,
    discharge_power REAL,
    vbat REAL,
    battery_temp REAL,
    bms_soh REAL,
    bms_cycles REAL,

    -- Load
    load_power REAL,
    load_today REAL,
    self_use_today REAL,

    -- Grid
    vac1 REAL,
    fac REAL,
    grid_export_today REAL,
    grid_import_today REAL,
    grid_import_power REAL,
    grid_export_power REAL,

    -- Inverter
    temp1 REAL,
    temp2 REAL,
    temp3 REAL,

    UNIQUE(device_sn, recorded_at)
);

CREATE INDEX IF NOT EXISTS idx_readings_device_time
    ON readings(device_sn, recorded_at);
`

const upsertSQL = `
INSERT INTO readings (
    recorded_at, device_sn, device_type, status_text,
    ppv_total, ppv1, ppv2, vpv1, vpv2, epv_today, epv_total,
    soc, charge_power, discharge_power, vbat, battery_temp, bms_soh, bms_cycles,
    load_power, load_today, self_use_today,
    vac1, fac, grid_export_today, grid_import_today, grid_import_power, grid_export_power,
    temp1, temp2, temp3
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(device_sn, recorded_at) DO UPDATE SET
    device_type=excluded.device_type,
    status_text=excluded.status_text,
    ppv_total=excluded.ppv_total, ppv1=excluded.ppv1, ppv2=excluded.ppv2,
    vpv1=excluded.vpv1, vpv2=excluded.vpv2,
    epv_today=excluded.epv_today, epv_total=excluded.epv_total,
    soc=excluded.soc, charge_power=excluded.charge_power, discharge_power=excluded.discharge_power,
    vbat=excluded.vbat, battery_temp=excluded.battery_temp, bms_soh=excluded.bms_soh, bms_cycles=excluded.bms_cycles,
    load_power=excluded.load_power, load_today=excluded.load_today, self_use_today=excluded.self_use_today,
    vac1=excluded.vac1, fac=excluded.fac,
    grid_export_today=excluded.grid_export_today, grid_import_today=excluded.grid_import_today,
    grid_import_power=excluded.grid_import_power, grid_export_power=excluded.grid_export_power,
    temp1=excluded.temp1, temp2=excluded.temp2, temp3=excluded.temp3
`

// Store handles SQLite storage and raw JSON archiving of device readings.
type Store struct {
	db         *sql.DB
	archiveDir string
}

// NewStore opens (or creates) a SQLite database and prepares the schema.
func NewStore(dbPath, archiveDir string) (*Store, error) {
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		return nil, fmt.Errorf("creating archive dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	return &Store{db: db, archiveDir: archiveDir}, nil
}

// UpsertReadings bulk-inserts or updates readings in a transaction.
// Returns the number of rows inserted and updated.
func (s *Store) UpsertReadings(deviceSN string, deviceType int, datas []map[string]any) (inserted, updated int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Check existing count before
	var beforeCount int
	err = tx.QueryRow("SELECT COUNT(*) FROM readings WHERE device_sn = ?", deviceSN).Scan(&beforeCount)
	if err != nil {
		return 0, 0, fmt.Errorf("counting existing: %w", err)
	}

	stmt, err := tx.Prepare(upsertSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("preparing upsert: %w", err)
	}
	defer stmt.Close()

	for _, data := range datas {
		recordedAt, err := parseAPITime(data)
		if err != nil {
			continue // skip data points without a valid timestamp
		}

		_, err = stmt.Exec(
			recordedAt,
			deviceSN,
			deviceType,
			mapGetStr(data, "statusText"),

			// Solar
			mapGetFloat(data, "ppv"),
			mapGetFloat(data, "ppv1"),
			mapGetFloat(data, "ppv2"),
			mapGetFloat(data, "vpv1"),
			mapGetFloat(data, "vpv2"),
			mapGetFloat(data, "epvtoday"),
			mapGetFloat(data, "epvTotal"),

			// Battery
			mapGetFloat(data, "soc"),
			mapGetFloat(data, "pcharge1"),
			mapGetFloat(data, "pdischarge1"),
			mapGetFloat(data, "vbat"),
			mapGetFloat(data, "batteryTemperature"),
			mapGetFloat(data, "bmsSOH"),
			mapGetFloat(data, "bmsCycleCnt"),

			// Load
			mapGetFloat(data, "plocalLoadTotal"),
			mapGetFloat(data, "elocalLoadToday"),
			mapGetFloat(data, "eselftoday"),

			// Grid
			mapGetFloat(data, "vac1"),
			mapGetFloat(data, "fac"),
			mapGetFloat(data, "etoGridToday"),
			mapGetFloat(data, "etoUserToday"),
			mapGetFloat(data, "pacToUserTotal"),
			mapGetFloat(data, "pacToGridTotal"),

			// Inverter
			mapGetFloat(data, "temp1"),
			mapGetFloat(data, "temp2"),
			mapGetFloat(data, "temp3"),
		)
		if err != nil {
			return 0, 0, fmt.Errorf("upserting reading at %s: %w", recordedAt, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("committing transaction: %w", err)
	}

	// Check count after to determine inserts vs updates
	var afterCount int
	err = s.db.QueryRow("SELECT COUNT(*) FROM readings WHERE device_sn = ?", deviceSN).Scan(&afterCount)
	if err != nil {
		return 0, 0, fmt.Errorf("counting after: %w", err)
	}

	inserted = afterCount - beforeCount
	updated = len(datas) - inserted
	return inserted, updated, nil
}

// ArchiveDayRaw saves the full raw API response for a device+date as one JSON file.
func (s *Store) ArchiveDayRaw(deviceSN, date string, datas []map[string]any) {
	raw, err := json.MarshalIndent(datas, "", "  ")
	if err != nil {
		return
	}
	filename := fmt.Sprintf("%s_%s.json", deviceSN, date)
	path := filepath.Join(s.archiveDir, filename)
	_ = os.WriteFile(path, raw, 0600)
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func parseAPITime(data map[string]any) (time.Time, error) {
	ts := mapGetStr(data, "time")
	if ts == "" {
		return time.Time{}, fmt.Errorf("no time field")
	}
	// Try common Growatt time formats
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
	} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time: %s", ts)
}

// MapGetFloat extracts a float64 from a map, returning 0 for missing/invalid values.
func MapGetFloat(data map[string]any, key string) float64 {
	return mapGetFloat(data, key)
}

func mapGetFloat(data map[string]any, key string) float64 {
	v, ok := data[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return 0
		}
		return val
	default:
		return 0
	}
}

// MapGetStr extracts a string from a map, returning "" for missing values.
func MapGetStr(data map[string]any, key string) string {
	return mapGetStr(data, key)
}

func mapGetStr(data map[string]any, key string) string {
	v, ok := data[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// TimeSeriesPoint represents a single time-series data point for charting.
type TimeSeriesPoint struct {
	Time            time.Time
	PPVTotal        float64
	LoadPower       float64
	SOC             float64
	ChargePower     float64
	DischargePower  float64
	GridImportPower float64
	GridExportPower float64
}

// BatteryPower returns net battery power (positive = discharging, negative = charging).
func (p TimeSeriesPoint) BatteryPower() float64 {
	if p.DischargePower > 0 {
		return p.DischargePower
	}
	if p.ChargePower > 0 {
		return -p.ChargePower
	}
	return 0
}

// QueryDayReadings returns time-series data points for a device on a given date.
func (s *Store) QueryDayReadings(deviceSN, date string) ([]TimeSeriesPoint, error) {
	// Use LIKE prefix match since timestamps are stored as "2026-03-27 00:01:18 +0000 UTC"
	rows, err := s.db.Query(`
		SELECT recorded_at, ppv_total, load_power, soc,
		       charge_power, discharge_power,
		       grid_import_power, grid_export_power
		FROM readings
		WHERE device_sn = ? AND recorded_at LIKE ?
		ORDER BY recorded_at`,
		deviceSN, date+"%")
	if err != nil {
		return nil, fmt.Errorf("querying readings: %w", err)
	}
	defer rows.Close()

	var points []TimeSeriesPoint
	for rows.Next() {
		var p TimeSeriesPoint
		var recordedAt string
		err := rows.Scan(&recordedAt, &p.PPVTotal, &p.LoadPower, &p.SOC,
			&p.ChargePower, &p.DischargePower,
			&p.GridImportPower, &p.GridExportPower)
		if err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		for _, layout := range []string{
			"2006-01-02 15:04:05 +0000 UTC",
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05Z",
		} {
			if t, err := time.Parse(layout, recordedAt); err == nil {
				p.Time = t
				break
			}
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// CostReadingPoint holds the fields needed for cost calculation.
type CostReadingPoint struct {
	Time            time.Time
	GridImportPower float64 // instantaneous watts
	GridExportPower float64 // instantaneous watts
	GridImportToday float64 // cumulative kWh
	GridExportToday float64 // cumulative kWh
}

// QueryRangeReadings returns cost-relevant data points for a device across a date range.
// The from and to parameters are date strings in YYYY-MM-DD format (inclusive).
func (s *Store) QueryRangeReadings(deviceSN, from, to string) ([]CostReadingPoint, error) {
	rows, err := s.db.Query(`
		SELECT recorded_at, grid_import_power, grid_export_power,
		       grid_import_today, grid_export_today
		FROM readings
		WHERE device_sn = ? AND recorded_at >= ? AND recorded_at < ?
		ORDER BY recorded_at`,
		deviceSN, from, to+" 99") // " 99" sorts after any time on the to date
	if err != nil {
		return nil, fmt.Errorf("querying range readings: %w", err)
	}
	defer rows.Close()

	var points []CostReadingPoint
	for rows.Next() {
		var p CostReadingPoint
		var recordedAt string
		var importPower, exportPower, importToday, exportToday sql.NullFloat64
		err := rows.Scan(&recordedAt, &importPower, &exportPower, &importToday, &exportToday)
		if err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		for _, layout := range []string{
			"2006-01-02 15:04:05 +0000 UTC",
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05Z",
		} {
			if t, err := time.Parse(layout, recordedAt); err == nil {
				p.Time = t
				break
			}
		}
		if importPower.Valid {
			p.GridImportPower = importPower.Float64
		}
		if exportPower.Valid {
			p.GridExportPower = exportPower.Float64
		}
		if importToday.Valid {
			p.GridImportToday = importToday.Float64
		}
		if exportToday.Valid {
			p.GridExportToday = exportToday.Float64
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// ListDeviceSNs returns all distinct device serial numbers in the database.
func (s *Store) ListDeviceSNs() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT device_sn FROM readings ORDER BY device_sn")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sns []string
	for rows.Next() {
		var sn string
		if err := rows.Scan(&sn); err != nil {
			return nil, err
		}
		sns = append(sns, sn)
	}
	return sns, rows.Err()
}

// ReadingsSummary returns a short summary string for a device reading.
func ReadingsSummary(deviceSN string, data map[string]any) string {
	ppv := mapGetFloat(data, "ppv")
	soc := mapGetFloat(data, "soc")
	load := mapGetFloat(data, "plocalLoadTotal")
	parts := []string{
		fmt.Sprintf("PV %.0fW", ppv),
		fmt.Sprintf("Battery %.0f%%", soc),
		fmt.Sprintf("Load %.0fW", load),
	}
	return fmt.Sprintf("%s: %s", deviceSN, strings.Join(parts, " | "))
}
