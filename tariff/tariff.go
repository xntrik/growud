package tariff

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds the full tariff configuration.
type Config struct {
	Timezone string   `json:"timezone"`
	Currency string   `json:"currency"`
	Import   []Window `json:"import"`
	Export   []Window `json:"export"`

	loc *time.Location
}

// Window defines a tariff rate for a specific time-of-day and day-of-week period.
type Window struct {
	Name        string   `json:"name"`
	CentsPerKWh float64  `json:"cents_per_kwh"`
	From        string   `json:"from"` // "HH:MM"
	To          string   `json:"to"`   // "HH:MM"
	Days        []string `json:"days"` // e.g. ["mon","tue",...] or ["all"]; empty = all
}

// LoadConfig reads and parses a tariff JSON config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading tariff config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing tariff config: %w", err)
	}

	if cfg.Timezone == "" {
		return nil, fmt.Errorf("tariff config: timezone is required")
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("tariff config: invalid timezone %q: %w", cfg.Timezone, err)
	}
	cfg.loc = loc

	// Validate all windows
	for i, w := range cfg.Import {
		if err := validateWindow(w); err != nil {
			return nil, fmt.Errorf("tariff config: import[%d] (%s): %w", i, w.Name, err)
		}
	}
	for i, w := range cfg.Export {
		if err := validateWindow(w); err != nil {
			return nil, fmt.Errorf("tariff config: export[%d] (%s): %w", i, w.Name, err)
		}
	}

	return &cfg, nil
}

// Location returns the parsed timezone location.
func (c *Config) Location() *time.Location {
	return c.loc
}

// MatchImportWindow returns the first import window that matches the given local time.
// Returns nil if no window matches.
func (c *Config) MatchImportWindow(t time.Time) *Window {
	lt := t.In(c.loc)
	for i := range c.Import {
		if windowMatches(&c.Import[i], lt) {
			return &c.Import[i]
		}
	}
	return nil
}

// MatchExportWindow returns the first export window that matches the given local time.
// Returns nil if no window matches.
func (c *Config) MatchExportWindow(t time.Time) *Window {
	lt := t.In(c.loc)
	for i := range c.Export {
		if windowMatches(&c.Export[i], lt) {
			return &c.Export[i]
		}
	}
	return nil
}

func validateWindow(w Window) error {
	if w.Name == "" {
		return fmt.Errorf("name is required")
	}
	if _, err := parseHHMM(w.From); err != nil {
		return fmt.Errorf("invalid from time %q: %w", w.From, err)
	}
	if _, err := parseHHMM(w.To); err != nil {
		return fmt.Errorf("invalid to time %q: %w", w.To, err)
	}
	for _, d := range w.Days {
		if !isValidDay(d) {
			return fmt.Errorf("invalid day %q", d)
		}
	}
	return nil
}

// windowMatches checks if a local time falls within a tariff window.
func windowMatches(w *Window, lt time.Time) bool {
	if !dayMatches(w.Days, lt.Weekday()) {
		return false
	}

	from, _ := parseHHMM(w.From)
	to, _ := parseHHMM(w.To)

	minuteOfDay := lt.Hour()*60 + lt.Minute()

	// "00:00" to "00:00" means all day
	if from == 0 && to == 0 {
		return true
	}

	if from < to {
		// Normal window: e.g. 07:00 to 14:00
		return minuteOfDay >= from && minuteOfDay < to
	}

	// Overnight window: e.g. 22:00 to 07:00
	return minuteOfDay >= from || minuteOfDay < to
}

// parseHHMM parses "HH:MM" into minutes since midnight.
func parseHHMM(s string) (int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("expected HH:MM format")
	}
	h, m := 0, 0
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return 0, err
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return 0, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("time out of range")
	}
	return h*60 + m, nil
}

var validDays = map[string]time.Weekday{
	"sun": time.Sunday,
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
}

func isValidDay(d string) bool {
	d = strings.ToLower(d)
	if d == "all" {
		return true
	}
	_, ok := validDays[d]
	return ok
}

func dayMatches(days []string, wd time.Weekday) bool {
	if len(days) == 0 {
		return true
	}
	for _, d := range days {
		d = strings.ToLower(d)
		if d == "all" {
			return true
		}
		if mapped, ok := validDays[d]; ok && mapped == wd {
			return true
		}
	}
	return false
}
