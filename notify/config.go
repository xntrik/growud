package notify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config is the full notification engine configuration, loaded from
// notifications.json. The whole feature is optional — if the file is absent,
// LoadConfig returns an error and callers simply run without notifications.
type Config struct {
	Timezone string   `json:"timezone"`
	Site     Site     `json:"location"`
	Targets  []Target `json:"targets"`
	Rules    []Rule   `json:"rules"`

	loc       *time.Location
	byName    map[string]*Target
	knownVars map[string]bool
}

// Site is the physical location of the plant, used for sun position.
type Site struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Target types.
const (
	TargetNtfy    = "ntfy"
	TargetWebhook = "webhook"
)

// Target is a place notifications get delivered to.
type Target struct {
	Name string `json:"name"`
	Type string `json:"type"` // "ntfy" or "webhook"
	URL  string `json:"url"`

	// AuthTokenEnv names an environment variable holding a bearer token.
	// The token itself is deliberately never stored in the config file.
	AuthTokenEnv string `json:"auth_token_env,omitempty"`

	// ntfy defaults, overridable per rule.
	Priority string   `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`

	// Generic webhook knobs, ignored by ntfy targets.
	Method       string            `json:"method,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	BodyTemplate string            `json:"body_template,omitempty"`
}

// Rule is one condition and the message it sends when that condition holds.
type Rule struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"` // absent means enabled
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`

	When        Condition   `json:"when"`
	OnlyBetween *TimeWindow `json:"only_between,omitempty"`

	// CooldownMinutes is the minimum gap between two notifications from this
	// rule. Repeat, when true, lets the rule fire on every evaluation the
	// condition holds (subject to the cooldown) rather than only on the
	// transition from false to true.
	CooldownMinutes int  `json:"cooldown_minutes,omitempty"`
	Repeat          bool `json:"repeat,omitempty"`

	// Targets names the targets to notify. Empty means all of them.
	Targets  []string `json:"targets,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// IsEnabled reports whether the rule should be evaluated.
func (r *Rule) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// TimeWindow restricts a rule to a time of day and set of weekdays.
type TimeWindow struct {
	From string   `json:"from"` // "HH:MM"
	To   string   `json:"to"`   // "HH:MM"
	Days []string `json:"days,omitempty"`
}

// ntfy accepts these priority names, or the numbers 1-5.
var validPriorities = map[string]bool{
	"min": true, "low": true, "default": true, "high": true, "urgent": true,
	"1": true, "2": true, "3": true, "4": true, "5": true,
}

// LoadConfig reads, parses and validates a notifications JSON config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading notification config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing notification config: %w", err)
	}

	if cfg.Timezone == "" {
		return nil, fmt.Errorf("notification config: timezone is required")
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("notification config: invalid timezone %q: %w", cfg.Timezone, err)
	}
	cfg.loc = loc

	if cfg.Site.Latitude < -90 || cfg.Site.Latitude > 90 {
		return nil, fmt.Errorf("notification config: latitude %v out of range (-90 to 90)", cfg.Site.Latitude)
	}
	if cfg.Site.Longitude < -180 || cfg.Site.Longitude > 180 {
		return nil, fmt.Errorf("notification config: longitude %v out of range (-180 to 180)", cfg.Site.Longitude)
	}

	if len(cfg.Targets) == 0 {
		return nil, fmt.Errorf("notification config: at least one target is required")
	}

	cfg.byName = make(map[string]*Target, len(cfg.Targets))
	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		if err := validateTarget(t); err != nil {
			return nil, fmt.Errorf("notification config: targets[%d] (%s): %w", i, t.Name, err)
		}
		if _, dup := cfg.byName[t.Name]; dup {
			return nil, fmt.Errorf("notification config: duplicate target name %q", t.Name)
		}
		cfg.byName[t.Name] = t
	}

	cfg.knownVars = templateVars()

	seenRules := make(map[string]bool, len(cfg.Rules))
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if err := cfg.validateRule(r); err != nil {
			return nil, fmt.Errorf("notification config: rules[%d] (%s): %w", i, r.Name, err)
		}
		if seenRules[r.Name] {
			return nil, fmt.Errorf("notification config: duplicate rule name %q", r.Name)
		}
		seenRules[r.Name] = true
	}

	return &cfg, nil
}

// TZ returns the parsed timezone location.
func (c *Config) TZ() *time.Location { return c.loc }

// Target looks up a target by name.
func (c *Config) Target(name string) *Target { return c.byName[name] }

// TargetsFor returns the targets a rule notifies. A rule with no explicit
// targets notifies all of them.
func (c *Config) TargetsFor(r *Rule) []*Target {
	if len(r.Targets) == 0 {
		out := make([]*Target, 0, len(c.Targets))
		for i := range c.Targets {
			out = append(out, &c.Targets[i])
		}
		return out
	}
	out := make([]*Target, 0, len(r.Targets))
	for _, name := range r.Targets {
		if t := c.byName[name]; t != nil {
			out = append(out, t)
		}
	}
	return out
}

func validateTarget(t *Target) error {
	if t.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch t.Type {
	case TargetNtfy, TargetWebhook:
	case "":
		return fmt.Errorf("type is required (%q or %q)", TargetNtfy, TargetWebhook)
	default:
		return fmt.Errorf("unknown type %q (want %q or %q)", t.Type, TargetNtfy, TargetWebhook)
	}

	if t.URL == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(t.URL)
	if err != nil {
		return fmt.Errorf("invalid url %q: %w", t.URL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url %q has no host", t.URL)
	}

	if t.Priority != "" && !validPriorities[strings.ToLower(t.Priority)] {
		return fmt.Errorf("invalid priority %q (min, low, default, high, urgent or 1-5)", t.Priority)
	}
	if t.Method != "" && !validMethod(t.Method) {
		return fmt.Errorf("invalid method %q", t.Method)
	}
	for k, v := range t.Headers {
		if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("header %q contains a line break", k)
		}
	}
	if err := validateTemplate(t.BodyTemplate, webhookTemplateVars()); err != nil {
		return fmt.Errorf("body_template: %w", err)
	}
	return nil
}

func validMethod(m string) bool {
	switch strings.ToUpper(m) {
	case "POST", "PUT", "PATCH", "GET":
		return true
	}
	return false
}

func (c *Config) validateRule(r *Rule) error {
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if r.Message == "" {
		return fmt.Errorf("message is required")
	}
	if err := r.When.validate(); err != nil {
		return fmt.Errorf("when: %w", err)
	}
	if r.OnlyBetween != nil {
		if err := r.OnlyBetween.validate(); err != nil {
			return fmt.Errorf("only_between: %w", err)
		}
	}
	if r.CooldownMinutes < 0 {
		return fmt.Errorf("cooldown_minutes must not be negative")
	}
	if r.Priority != "" && !validPriorities[strings.ToLower(r.Priority)] {
		return fmt.Errorf("invalid priority %q (min, low, default, high, urgent or 1-5)", r.Priority)
	}
	for _, name := range r.Targets {
		if c.byName[name] == nil {
			return fmt.Errorf("unknown target %q", name)
		}
	}
	for _, field := range []struct{ label, tmpl string }{
		{"message", r.Message},
		{"title", r.Title},
	} {
		if err := validateTemplate(field.tmpl, c.knownVars); err != nil {
			return fmt.Errorf("%s: %w", field.label, err)
		}
	}
	return nil
}

func (w *TimeWindow) validate() error {
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

// Matches reports whether a local time falls inside the window. A window of
// "00:00" to "00:00" covers the whole day, and windows that wrap past midnight
// (e.g. 22:00 to 07:00) work as you would expect.
func (w *TimeWindow) Matches(lt time.Time) bool {
	if w == nil {
		return true
	}
	if !dayMatches(w.Days, lt.Weekday()) {
		return false
	}

	from, _ := parseHHMM(w.From)
	to, _ := parseHHMM(w.To)
	minuteOfDay := lt.Hour()*60 + lt.Minute()

	if from == 0 && to == 0 {
		return true
	}
	if from < to {
		return minuteOfDay >= from && minuteOfDay < to
	}
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
