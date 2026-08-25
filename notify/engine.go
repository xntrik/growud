package notify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Result records what the engine decided about a single rule in one pass.
type Result struct {
	Rule    *Rule
	Matched bool   // the condition held
	Fired   bool   // a notification actually went out
	Skipped string // why a matching rule did not fire

	Notification Notification
	Errors       []error
}

// Engine evaluates rules against live device data and dispatches the ones
// that fire.
type Engine struct {
	cfg    *Config
	state  *State
	sender *Sender
	logf   func(format string, args ...any)

	// mu serialises evaluation passes. The tray refreshes from more than one
	// goroutine, and two overlapping passes could each decide to notify.
	mu sync.Mutex
}

// NewEngine creates an engine backed by the state file at statePath. An empty
// statePath keeps state in memory only, which is what the dry run wants.
func NewEngine(cfg *Config, statePath string) *Engine {
	return &Engine{
		cfg:    cfg,
		state:  LoadState(statePath),
		sender: NewSender(),
		logf:   log.Printf,
	}
}

// SetLogger overrides where the engine reports delivery successes and failures.
func (e *Engine) SetLogger(logf func(format string, args ...any)) {
	if logf != nil {
		e.logf = logf
	}
}

// Config returns the loaded configuration.
func (e *Engine) Config() *Config { return e.cfg }

// Evaluate runs every rule and reports what would happen, without sending
// anything or touching persisted state.
func (e *Engine) Evaluate(deviceSN string, data map[string]any, now time.Time) (Snapshot, []Result) {
	return e.evaluate(context.Background(), deviceSN, data, now, false)
}

// Run evaluates every rule, dispatches the ones that fire, and persists state.
func (e *Engine) Run(ctx context.Context, deviceSN string, data map[string]any, now time.Time) (Snapshot, []Result) {
	return e.evaluate(ctx, deviceSN, data, now, true)
}

func (e *Engine) evaluate(ctx context.Context, deviceSN string, data map[string]any, now time.Time, dispatch bool) (Snapshot, []Result) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Another growud process may have fired some of these rules since the last
	// pass; pick up its state before deciding anything.
	e.state.Reload()

	snap := BuildSnapshot(e.cfg, deviceSN, data, now)
	results := make([]Result, 0, len(e.cfg.Rules))
	dirty := false

	for i := range e.cfg.Rules {
		r := &e.cfg.Rules[i]
		res := Result{Rule: r}

		if !r.IsEnabled() {
			res.Skipped = "disabled"
			results = append(results, res)
			continue
		}

		// Outside its schedule a rule is not evaluated at all, and its
		// remembered state is deliberately left alone — so a condition that
		// first holds at 3am still fires when the window opens in the morning.
		if !r.OnlyBetween.Matches(snap.At) {
			res.Skipped = "outside only_between window"
			results = append(results, res)
			continue
		}

		res.Matched = r.When.Eval(snap)
		prev := e.state.Get(r.Name)

		if !res.Matched {
			if prev.LastMatched && dispatch {
				prev.LastMatched = false
				e.state.Set(r.Name, prev)
				dirty = true
			}
			results = append(results, res)
			continue
		}

		switch {
		case !r.Repeat && prev.LastMatched:
			res.Skipped = "already notified; condition has not gone false since"
		case r.CooldownMinutes > 0 && !prev.LastFired.IsZero():
			cooldown := time.Duration(r.CooldownMinutes) * time.Minute
			if elapsed := now.Sub(prev.LastFired); elapsed < cooldown {
				res.Skipped = fmt.Sprintf("cooling down for another %s", (cooldown - elapsed).Round(time.Minute))
			}
		}

		res.Notification = e.buildNotification(r, snap)

		if res.Skipped != "" || !dispatch {
			results = append(results, res)
			continue
		}

		sent, errs := e.dispatch(ctx, r, res.Notification)
		res.Errors = errs
		if sent {
			res.Fired = true
			e.state.Set(r.Name, RuleState{LastFired: now, LastMatched: true})
			dirty = true
		}
		// When every target failed the state is left untouched on purpose, so
		// the next pass retries instead of quietly dropping the alert.
		results = append(results, res)
	}

	if dispatch && dirty {
		if err := e.state.Save(); err != nil {
			e.logf("notify: saving state: %v", err)
		}
	}

	return snap, results
}

func (e *Engine) buildNotification(r *Rule, snap Snapshot) Notification {
	title := r.Title
	if title == "" {
		title = defaultTitle(r.Name)
	}

	fields := renderFields(snap, r.Name, title)
	title = Render(title, fields)
	fields["title"] = title
	message := Render(r.Message, fields)

	metrics := make(map[string]float64, len(snap.Values))
	for k, v := range snap.Values {
		metrics[k] = v
	}

	return Notification{
		Rule:     r.Name,
		Title:    title,
		Message:  message,
		Device:   snap.DeviceSN,
		At:       snap.At,
		Priority: r.Priority,
		Tags:     r.Tags,
		Metrics:  metrics,
		Fields:   fields,
	}
}

// dispatch sends to every target the rule names. It reports success if at
// least one target accepted the notification.
func (e *Engine) dispatch(ctx context.Context, r *Rule, n Notification) (sent bool, errs []error) {
	targets := e.cfg.TargetsFor(r)
	if len(targets) == 0 {
		return false, []error{fmt.Errorf("rule %q resolves to no targets", r.Name)}
	}

	for _, t := range targets {
		if err := e.sender.Send(ctx, t, n); err != nil {
			errs = append(errs, err)
			e.logf("notify: rule %q -> target %q failed: %v", r.Name, t.Name, err)
			continue
		}
		sent = true
		e.logf("notify: rule %q -> target %q delivered", r.Name, t.Name)
	}
	return sent, errs
}

// SendTest delivers a fixed message to every configured target, so an ntfy
// topic or webhook URL can be verified without waiting for a rule to fire.
func (e *Engine) SendTest(ctx context.Context) []error {
	n := Notification{
		Rule:    "test",
		Title:   "Growud test",
		Message: "Growud notifications are configured correctly.",
		At:      time.Now().In(e.cfg.TZ()),
		Metrics: map[string]float64{},
		Fields:  map[string]string{"rule": "test", "title": "Growud test"},
	}

	var errs []error
	for i := range e.cfg.Targets {
		t := &e.cfg.Targets[i]
		if err := e.sender.Send(ctx, t, n); err != nil {
			errs = append(errs, fmt.Errorf("target %s: %w", t.Name, err))
			continue
		}
		e.logf("notify: test message delivered to %q", t.Name)
	}
	return errs
}

// defaultTitle turns a rule name like "ev_charge_window" into "Ev charge window".
func defaultTitle(name string) string {
	spaced := strings.ReplaceAll(name, "_", " ")
	runes := []rune(spaced)
	if len(runes) == 0 {
		return "Growud"
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
