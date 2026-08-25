package notify

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// placeholderRe matches {{name}}, tolerating whitespace inside the braces.
var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// contextVars are the non-metric placeholders available in every template.
var contextVars = []string{"rule", "title", "device", "time", "date", "sunrise", "sunset"}

// templateVars is the set of placeholders valid in a rule's title and message.
func templateVars() map[string]bool {
	vars := make(map[string]bool)
	for _, name := range MetricNames() {
		vars[name] = true
	}
	for _, name := range contextVars {
		vars[name] = true
	}
	return vars
}

// webhookTemplateVars additionally allows {{message}}, which is only
// meaningful once the rule's own message has been rendered.
func webhookTemplateVars() map[string]bool {
	vars := templateVars()
	vars["message"] = true
	return vars
}

// validateTemplate rejects placeholders that will never resolve, so a typo in
// a rule shows up when the config loads instead of inside a notification.
func validateTemplate(tmpl string, known map[string]bool) error {
	for _, m := range placeholderRe.FindAllStringSubmatch(tmpl, -1) {
		if !known[m[1]] {
			return fmt.Errorf("unknown placeholder {{%s}} (known: %s)", m[1], strings.Join(sortedKeys(known), ", "))
		}
	}
	return nil
}

// Render substitutes {{name}} placeholders from fields. Placeholders with no
// matching field are left untouched rather than blanked out.
func Render(tmpl string, fields map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := placeholderRe.FindStringSubmatch(match)[1]
		if v, ok := fields[name]; ok {
			return v
		}
		return match
	})
}

// renderFields builds the placeholder context for a snapshot and rule.
func renderFields(s Snapshot, ruleName, title string) map[string]string {
	fields := make(map[string]string, len(s.Values)+len(contextVars))
	for name, v := range s.Values {
		fields[name] = formatNumber(v)
	}
	fields["rule"] = ruleName
	fields["title"] = title
	fields["device"] = s.DeviceSN
	fields["time"] = s.At.Format("15:04")
	fields["date"] = s.At.Format("2006-01-02")
	fields["sunrise"] = formatClock(s.Sunrise)
	fields["sunset"] = formatClock(s.Sunset)
	return fields
}

func formatClock(t time.Time) string {
	if t.IsZero() {
		return "--:--"
	}
	return t.Format("15:04")
}

// formatNumber renders a metric for display: one decimal place, with a
// trailing ".0" trimmed so whole numbers read as "97" rather than "97.0".
func formatNumber(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	if math.Abs(v) < 0.05 {
		return "0"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
