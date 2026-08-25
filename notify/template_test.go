package notify

import (
	"testing"
	"time"
)

func TestRender(t *testing.T) {
	fields := map[string]string{
		"battery_soc":        "96",
		"hours_until_sunset": "2.5",
		"device":             "ABC123",
	}

	tests := []struct {
		tmpl string
		want string
	}{
		{"Battery at {{battery_soc}}%", "Battery at 96%"},
		{"{{ battery_soc }} and {{hours_until_sunset}}h", "96 and 2.5h"},
		{"no placeholders", "no placeholders"},
		{"{{device}}: {{battery_soc}}%", "ABC123: 96%"},
		// An unresolved placeholder is left alone rather than blanked, so the
		// mistake is visible rather than silently swallowed.
		{"{{unknown}}", "{{unknown}}"},
	}

	for _, tt := range tests {
		if got := Render(tt.tmpl, fields); got != tt.want {
			t.Errorf("Render(%q) = %q, want %q", tt.tmpl, got, tt.want)
		}
	}
}

func TestValidateTemplate(t *testing.T) {
	known := templateVars()

	if err := validateTemplate("Battery at {{battery_soc}}% with {{hours_until_sunset}}h left", known); err != nil {
		t.Errorf("unexpected error for valid template: %v", err)
	}
	if err := validateTemplate("{{device}} at {{time}} on {{date}}, sunset {{sunset}}", known); err != nil {
		t.Errorf("unexpected error for context placeholders: %v", err)
	}
	if err := validateTemplate("{{battery_percent}}", known); err == nil {
		t.Error("expected an error for an unknown placeholder")
	}
	// {{message}} only makes sense in a webhook body template.
	if err := validateTemplate("{{message}}", known); err == nil {
		t.Error("expected {{message}} to be rejected in a rule template")
	}
	if err := validateTemplate("{{message}}", webhookTemplateVars()); err != nil {
		t.Errorf("expected {{message}} to be valid in a body template: %v", err)
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{96, "96"},
		{96.5, "96.5"},
		{96.04, "96"},
		{3200, "3200"},
		{2.46, "2.5"},
		{0, "0"},
		{-0.01, "0"},
		{-15.5, "-15.5"},
	}

	for _, tt := range tests {
		if got := formatNumber(tt.in); got != tt.want {
			t.Errorf("formatNumber(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderFields(t *testing.T) {
	at := time.Date(2026, 8, 25, 14, 30, 0, 0, time.UTC)
	snap := Snapshot{
		DeviceSN: "SN123",
		At:       at,
		Sunrise:  time.Date(2026, 8, 25, 6, 15, 0, 0, time.UTC),
		Sunset:   time.Date(2026, 8, 25, 17, 45, 0, 0, time.UTC),
		Values:   map[string]float64{"battery_soc": 96.2},
	}

	fields := renderFields(snap, "my_rule", "My title")

	want := map[string]string{
		"battery_soc": "96.2",
		"rule":        "my_rule",
		"title":       "My title",
		"device":      "SN123",
		"time":        "14:30",
		"date":        "2026-08-25",
		"sunrise":     "06:15",
		"sunset":      "17:45",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("fields[%q] = %q, want %q", k, fields[k], v)
		}
	}
}

func TestFormatClock_ZeroTime(t *testing.T) {
	// Polar days leave sunrise/sunset unset; the template should not print
	// a year-one date.
	if got := formatClock(time.Time{}); got != "--:--" {
		t.Errorf("formatClock(zero) = %q, want %q", got, "--:--")
	}
}
