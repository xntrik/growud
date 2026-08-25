package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a target endpoint that counts requests and can be told to fail.
type recorder struct {
	mu     sync.Mutex
	bodies []string
	titles []string
	status int
}

func newRecorder(t *testing.T) (*recorder, string) {
	t.Helper()
	r := &recorder{status: http.StatusOK}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, string(body))
		r.titles = append(r.titles, req.Header.Get("Title"))
		status := r.status
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return r, srv.URL
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *recorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return ""
	}
	return r.bodies[len(r.bodies)-1]
}

func (r *recorder) setStatus(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

// newEngine builds an engine from a config body with %s substituted for the
// recorder's URL.
func newEngine(t *testing.T, url, rulesJSON, statePath string) *Engine {
	t.Helper()
	body := fmt.Sprintf(`{
	  "timezone": "UTC",
	  "location": {"latitude": -33.8688, "longitude": 151.2093},
	  "targets": [{"name": "phone", "type": "ntfy", "url": %q}],
	  "rules": %s
	}`, url, rulesJSON)

	cfg, err := LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	e := NewEngine(cfg, statePath)
	e.SetLogger(func(string, ...any) {})
	return e
}

const socRule = `[{
  "name": "battery_full",
  "title": "Battery at {{battery_soc}}%",
  "message": "Battery {{battery_soc}}%, solar {{solar_power}}W on {{device}}.",
  "when": {"metric": "battery_soc", "op": ">=", "value": 95}
}]`

func data(soc, solar float64) map[string]any {
	return map[string]any{"soc": soc, "ppv": solar}
}

func resultFor(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Rule.Name == name {
			return r
		}
	}
	t.Fatalf("no result for rule %q", name)
	return Result{}
}

func TestEngine_FiresAndRendersMessage(t *testing.T) {
	rec, url := newRecorder(t)
	e := newEngine(t, url, socRule, filepath.Join(t.TempDir(), "state.json"))

	_, results := e.Run(context.Background(), "SN123", data(96, 3200), time.Now())

	res := resultFor(t, results, "battery_full")
	if !res.Matched || !res.Fired {
		t.Fatalf("result = %+v, want matched and fired", res)
	}
	if len(res.Errors) != 0 {
		t.Errorf("errors = %v", res.Errors)
	}

	if rec.count() != 1 {
		t.Fatalf("sent %d notifications, want 1", rec.count())
	}
	if got, want := rec.last(), "Battery 96%, solar 3200W on SN123."; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if got, want := rec.titles[0], "Battery at 96%"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
}

func TestEngine_DoesNotFireWhenConditionIsFalse(t *testing.T) {
	rec, url := newRecorder(t)
	e := newEngine(t, url, socRule, filepath.Join(t.TempDir(), "state.json"))

	_, results := e.Run(context.Background(), "SN123", data(50, 0), time.Now())

	if res := resultFor(t, results, "battery_full"); res.Matched || res.Fired {
		t.Errorf("result = %+v, want neither matched nor fired", res)
	}
	if rec.count() != 0 {
		t.Errorf("sent %d notifications, want 0", rec.count())
	}
}

// TestEngine_EdgeTriggered is the anti-spam behaviour: a rule whose condition
// stays true does not notify again until it has gone false.
func TestEngine_EdgeTriggered(t *testing.T) {
	rec, url := newRecorder(t)
	e := newEngine(t, url, socRule, filepath.Join(t.TempDir(), "state.json"))
	now := time.Now()

	e.Run(context.Background(), "SN123", data(96, 3200), now)
	if rec.count() != 1 {
		t.Fatalf("first pass sent %d, want 1", rec.count())
	}

	// Still true five minutes later: no second notification.
	_, results := e.Run(context.Background(), "SN123", data(97, 3100), now.Add(5*time.Minute))
	res := resultFor(t, results, "battery_full")
	if !res.Matched {
		t.Error("expected the condition to still hold")
	}
	if res.Fired {
		t.Error("expected no second notification while the condition stayed true")
	}
	if res.Skipped == "" {
		t.Error("expected a reason for the skip")
	}
	if rec.count() != 1 {
		t.Fatalf("sent %d notifications, want 1", rec.count())
	}

	// Condition goes false, then true again: it fires a second time.
	e.Run(context.Background(), "SN123", data(60, 0), now.Add(60*time.Minute))
	e.Run(context.Background(), "SN123", data(96, 2000), now.Add(120*time.Minute))
	if rec.count() != 2 {
		t.Errorf("sent %d notifications, want 2 after the condition reset", rec.count())
	}
}

func TestEngine_RepeatWithCooldown(t *testing.T) {
	rec, url := newRecorder(t)
	rules := `[{
	  "name": "battery_full",
	  "message": "Battery {{battery_soc}}%",
	  "when": {"metric": "battery_soc", "op": ">=", "value": 95},
	  "repeat": true,
	  "cooldown_minutes": 60
	}]`
	e := newEngine(t, url, rules, filepath.Join(t.TempDir(), "state.json"))
	now := time.Now()

	e.Run(context.Background(), "SN123", data(96, 3200), now)
	if rec.count() != 1 {
		t.Fatalf("first pass sent %d, want 1", rec.count())
	}

	// Inside the cooldown: skipped, with the remaining time explained.
	_, results := e.Run(context.Background(), "SN123", data(96, 3200), now.Add(30*time.Minute))
	res := resultFor(t, results, "battery_full")
	if res.Fired {
		t.Error("expected no notification inside the cooldown")
	}
	if !strings.Contains(res.Skipped, "cooling down") {
		t.Errorf("skip reason = %q, want it to mention the cooldown", res.Skipped)
	}
	if rec.count() != 1 {
		t.Fatalf("sent %d notifications during the cooldown, want 1", rec.count())
	}

	// Past the cooldown, a repeating rule fires again without the condition
	// having gone false.
	e.Run(context.Background(), "SN123", data(96, 3200), now.Add(61*time.Minute))
	if rec.count() != 2 {
		t.Errorf("sent %d notifications, want 2 after the cooldown expired", rec.count())
	}
}

func TestEngine_OnlyBetweenBlocksFiring(t *testing.T) {
	rec, url := newRecorder(t)
	rules := `[{
	  "name": "battery_full",
	  "message": "Battery {{battery_soc}}%",
	  "when": {"metric": "battery_soc", "op": ">=", "value": 95},
	  "only_between": {"from": "08:00", "to": "18:00"}
	}]`
	statePath := filepath.Join(t.TempDir(), "state.json")
	e := newEngine(t, url, rules, statePath)

	// 3am: outside the window, so nothing is sent.
	night := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)
	_, results := e.Run(context.Background(), "SN123", data(96, 0), night)
	if res := resultFor(t, results, "battery_full"); res.Fired || !strings.Contains(res.Skipped, "only_between") {
		t.Errorf("result = %+v, want skipped for being outside the window", res)
	}
	if rec.count() != 0 {
		t.Fatalf("sent %d notifications at 3am, want 0", rec.count())
	}

	// The same still-true condition fires once the window opens, because the
	// out-of-hours pass deliberately left the remembered state alone.
	morning := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	e.Run(context.Background(), "SN123", data(96, 3200), morning)
	if rec.count() != 1 {
		t.Errorf("sent %d notifications once the window opened, want 1", rec.count())
	}
}

func TestEngine_DisabledRule(t *testing.T) {
	rec, url := newRecorder(t)
	rules := `[{
	  "name": "battery_full",
	  "enabled": false,
	  "message": "Battery {{battery_soc}}%",
	  "when": {"metric": "battery_soc", "op": ">=", "value": 95}
	}]`
	e := newEngine(t, url, rules, filepath.Join(t.TempDir(), "state.json"))

	_, results := e.Run(context.Background(), "SN123", data(96, 3200), time.Now())

	if res := resultFor(t, results, "battery_full"); res.Fired || res.Skipped != "disabled" {
		t.Errorf("result = %+v, want skipped as disabled", res)
	}
	if rec.count() != 0 {
		t.Errorf("sent %d notifications for a disabled rule, want 0", rec.count())
	}
}

// TestEngine_FailedDeliveryRetriesNextPass covers the case where the target is
// unreachable: the rule must not be marked as notified, or the alert is lost.
func TestEngine_FailedDeliveryRetriesNextPass(t *testing.T) {
	rec, url := newRecorder(t)
	e := newEngine(t, url, socRule, filepath.Join(t.TempDir(), "state.json"))
	now := time.Now()

	rec.setStatus(http.StatusInternalServerError)
	_, results := e.Run(context.Background(), "SN123", data(96, 3200), now)

	res := resultFor(t, results, "battery_full")
	if res.Fired {
		t.Error("Fired = true despite the target returning 500")
	}
	if len(res.Errors) == 0 {
		t.Error("expected the delivery error to be reported")
	}

	// The target recovers, and the next pass delivers rather than treating the
	// rule as already notified.
	rec.setStatus(http.StatusOK)
	_, results = e.Run(context.Background(), "SN123", data(96, 3200), now.Add(5*time.Minute))
	if res := resultFor(t, results, "battery_full"); !res.Fired {
		t.Errorf("result = %+v, want the retry to fire", res)
	}
}

func TestEngine_EvaluateSendsNothing(t *testing.T) {
	rec, url := newRecorder(t)
	statePath := filepath.Join(t.TempDir(), "state.json")
	e := newEngine(t, url, socRule, statePath)

	snap, results := e.Evaluate("SN123", data(96, 3200), time.Now())

	res := resultFor(t, results, "battery_full")
	if !res.Matched {
		t.Error("expected the condition to match")
	}
	if res.Fired {
		t.Error("Evaluate() should never mark a rule as fired")
	}
	if res.Notification.Message == "" {
		t.Error("expected Evaluate() to still render the message for previewing")
	}
	if snap.Values["battery_soc"] != 96 {
		t.Errorf("snapshot battery_soc = %v", snap.Values["battery_soc"])
	}

	if rec.count() != 0 {
		t.Errorf("Evaluate() sent %d notifications, want 0", rec.count())
	}
	if _, err := os.Stat(statePath); err == nil {
		t.Error("Evaluate() should not write the state file")
	}
}

// TestEngine_StatePersistsAcrossRestarts makes sure the cooldown and
// edge-trigger history survive the app being restarted.
func TestEngine_StatePersistsAcrossRestarts(t *testing.T) {
	rec, url := newRecorder(t)
	statePath := filepath.Join(t.TempDir(), "state.json")
	now := time.Now()

	first := newEngine(t, url, socRule, statePath)
	first.Run(context.Background(), "SN123", data(96, 3200), now)
	if rec.count() != 1 {
		t.Fatalf("first engine sent %d, want 1", rec.count())
	}

	// A fresh engine reading the same state file must not re-notify.
	second := newEngine(t, url, socRule, statePath)
	second.Run(context.Background(), "SN123", data(96, 3200), now.Add(5*time.Minute))
	if rec.count() != 1 {
		t.Errorf("sent %d notifications after a restart, want 1", rec.count())
	}
}

func TestEngine_SendTest(t *testing.T) {
	rec, url := newRecorder(t)
	e := newEngine(t, url, `[]`, "")

	if errs := e.SendTest(context.Background()); len(errs) != 0 {
		t.Fatalf("SendTest() errors = %v", errs)
	}
	if rec.count() != 1 {
		t.Errorf("sent %d test notifications, want 1", rec.count())
	}

	rec.setStatus(http.StatusUnauthorized)
	if errs := e.SendTest(context.Background()); len(errs) != 1 {
		t.Errorf("SendTest() errors = %v, want one error", errs)
	}
}

func TestDefaultTitle(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ev_charge_window", "Ev charge window"},
		{"battery_full", "Battery full"},
		{"simple", "Simple"},
		{"", "Growud"},
	}
	for _, tt := range tests {
		if got := defaultTitle(tt.in); got != tt.want {
			t.Errorf("defaultTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
