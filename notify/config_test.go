package notify

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const validConfig = `{
  "timezone": "UTC",
  "location": {"latitude": -31.95, "longitude": 115.86},
  "targets": [
    {"name": "phone", "type": "ntfy", "url": "https://ntfy.sh/growud-test", "priority": "high"}
  ],
  "rules": [
    {
      "name": "ev_charge_window",
      "message": "Battery at {{battery_soc}}% with {{hours_until_sunset}}h of sun left.",
      "when": {
        "all": [
          {"metric": "battery_soc", "op": ">=", "value": 95},
          {"metric": "minutes_until_sunset", "op": ">=", "value": 150}
        ]
      },
      "cooldown_minutes": 180,
      "targets": ["phone"]
    }
  ]
}`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notifications.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestLoadConfig_Valid(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.TZ() != time.UTC {
		t.Errorf("TZ() = %v, want UTC", cfg.TZ())
	}
	if cfg.Site.Latitude != -31.95 || cfg.Site.Longitude != 115.86 {
		t.Errorf("Site = %+v", cfg.Site)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].Name != "ev_charge_window" {
		t.Fatalf("Rules = %+v", cfg.Rules)
	}
	if !cfg.Rules[0].IsEnabled() {
		t.Error("a rule with no explicit enabled flag should be enabled")
	}
	if cfg.Target("phone") == nil {
		t.Error("Target(\"phone\") = nil")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("expected an error for a missing config file")
	}
}

func TestLoadConfig_Invalid(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed json", `{"timezone":`},
		{"no timezone", `{"location":{},"targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[]}`},
		{"bad timezone", `{"timezone":"Mars/Olympus","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[]}`},
		{"latitude out of range", `{"timezone":"UTC","location":{"latitude":95},"targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[]}`},
		{"longitude out of range", `{"timezone":"UTC","location":{"longitude":-200},"targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[]}`},
		{"no targets", `{"timezone":"UTC","targets":[],"rules":[]}`},
		{"target without type", `{"timezone":"UTC","targets":[{"name":"a","url":"https://ntfy.sh/x"}],"rules":[]}`},
		{"unknown target type", `{"timezone":"UTC","targets":[{"name":"a","type":"carrier-pigeon","url":"https://ntfy.sh/x"}],"rules":[]}`},
		{"non-http url", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"file:///etc/passwd"}],"rules":[]}`},
		{"url without host", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://"}],"rules":[]}`},
		{"bad priority", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x","priority":"screaming"}],"rules":[]}`},
		{"bad method", `{"timezone":"UTC","targets":[{"name":"a","type":"webhook","url":"https://x.test/h","method":"DELETE"}],"rules":[]}`},
		{"header with newline", `{"timezone":"UTC","targets":[{"name":"a","type":"webhook","url":"https://x.test/h","headers":{"X-A":"b\nX-Evil: c"}}],"rules":[]}`},
		{"duplicate target", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"},{"name":"a","type":"ntfy","url":"https://ntfy.sh/y"}],"rules":[]}`},
		{"rule without message", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"name":"r","when":{"metric":"battery_soc","op":">","value":1}}]}`},
		{"rule without name", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"message":"hi","when":{"metric":"battery_soc","op":">","value":1}}]}`},
		{"rule with unknown metric", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"name":"r","message":"hi","when":{"metric":"battery_percent","op":">","value":1}}]}`},
		{"rule with unknown target", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"name":"r","message":"hi","when":{"metric":"battery_soc","op":">","value":1},"targets":["b"]}]}`},
		{"rule with unknown placeholder", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"name":"r","message":"{{battery_percent}}","when":{"metric":"battery_soc","op":">","value":1}}]}`},
		{"rule with bad window", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"name":"r","message":"hi","when":{"metric":"battery_soc","op":">","value":1},"only_between":{"from":"25:00","to":"09:00"}}]}`},
		{"rule with bad day", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"name":"r","message":"hi","when":{"metric":"battery_soc","op":">","value":1},"only_between":{"from":"08:00","to":"09:00","days":["funday"]}}]}`},
		{"negative cooldown", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"name":"r","message":"hi","when":{"metric":"battery_soc","op":">","value":1},"cooldown_minutes":-5}]}`},
		{"duplicate rule", `{"timezone":"UTC","targets":[{"name":"a","type":"ntfy","url":"https://ntfy.sh/x"}],"rules":[{"name":"r","message":"hi","when":{"metric":"battery_soc","op":">","value":1}},{"name":"r","message":"hi","when":{"metric":"battery_soc","op":">","value":1}}]}`},
		{"bad body template", `{"timezone":"UTC","targets":[{"name":"a","type":"webhook","url":"https://x.test/h","body_template":"{{nope}}"}],"rules":[]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfig(t, tt.body)); err == nil {
				t.Errorf("expected an error for %s", tt.name)
			}
		})
	}
}

func TestConfig_TargetsFor(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `{
	  "timezone": "UTC",
	  "targets": [
	    {"name": "phone", "type": "ntfy", "url": "https://ntfy.sh/x"},
	    {"name": "hass", "type": "webhook", "url": "https://hass.test/hook"}
	  ],
	  "rules": [
	    {"name": "explicit", "message": "hi", "when": {"metric": "battery_soc", "op": ">", "value": 1}, "targets": ["hass"]},
	    {"name": "implicit", "message": "hi", "when": {"metric": "battery_soc", "op": ">", "value": 1}}
	  ]
	}`))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	explicit := cfg.TargetsFor(&cfg.Rules[0])
	if len(explicit) != 1 || explicit[0].Name != "hass" {
		t.Errorf("explicit targets = %+v, want just hass", explicit)
	}

	// A rule with no targets listed goes to every target.
	implicit := cfg.TargetsFor(&cfg.Rules[1])
	if len(implicit) != 2 {
		t.Errorf("implicit targets = %d, want 2", len(implicit))
	}
}

func TestTimeWindow_Matches(t *testing.T) {
	at := func(hour, minute int, weekday time.Weekday) time.Time {
		// 2026-08-24 is a Monday.
		day := 24 + int(weekday-time.Monday)
		return time.Date(2026, 8, day, hour, minute, 0, 0, time.UTC)
	}

	tests := []struct {
		name   string
		window *TimeWindow
		when   time.Time
		want   bool
	}{
		{"nil window matches everything", nil, at(3, 0, time.Monday), true},
		{"inside", &TimeWindow{From: "08:00", To: "18:00"}, at(12, 0, time.Monday), true},
		{"before", &TimeWindow{From: "08:00", To: "18:00"}, at(7, 59, time.Monday), false},
		{"at start", &TimeWindow{From: "08:00", To: "18:00"}, at(8, 0, time.Monday), true},
		{"at end is exclusive", &TimeWindow{From: "08:00", To: "18:00"}, at(18, 0, time.Monday), false},
		{"all day", &TimeWindow{From: "00:00", To: "00:00"}, at(3, 0, time.Monday), true},
		{"overnight before midnight", &TimeWindow{From: "22:00", To: "07:00"}, at(23, 0, time.Monday), true},
		{"overnight after midnight", &TimeWindow{From: "22:00", To: "07:00"}, at(2, 0, time.Monday), true},
		{"overnight outside", &TimeWindow{From: "22:00", To: "07:00"}, at(12, 0, time.Monday), false},
		{"matching day", &TimeWindow{From: "08:00", To: "18:00", Days: []string{"mon"}}, at(12, 0, time.Monday), true},
		{"other day", &TimeWindow{From: "08:00", To: "18:00", Days: []string{"sat", "sun"}}, at(12, 0, time.Monday), false},
		{"all days", &TimeWindow{From: "08:00", To: "18:00", Days: []string{"all"}}, at(12, 0, time.Wednesday), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.window.Matches(tt.when); got != tt.want {
				t.Errorf("Matches(%v) = %v, want %v", tt.when, got, tt.want)
			}
		})
	}
}

func TestRule_IsEnabled(t *testing.T) {
	disabled := false
	enabled := true

	if (&Rule{}).IsEnabled() != true {
		t.Error("a rule with no enabled field should default to enabled")
	}
	if (&Rule{Enabled: &disabled}).IsEnabled() != false {
		t.Error("enabled:false should disable the rule")
	}
	if (&Rule{Enabled: &enabled}).IsEnabled() != true {
		t.Error("enabled:true should enable the rule")
	}
}
