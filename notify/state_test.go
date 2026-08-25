package notify

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestState_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify_state.json")
	fired := time.Date(2026, 8, 25, 14, 30, 0, 0, time.UTC)

	s := LoadState(path)
	s.Set("ev_charge_window", RuleState{LastFired: fired, LastMatched: true})
	if err := s.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded := LoadState(path)
	got := reloaded.Get("ev_charge_window")
	if !got.LastFired.Equal(fired) {
		t.Errorf("LastFired = %v, want %v", got.LastFired, fired)
	}
	if !got.LastMatched {
		t.Error("LastMatched = false, want true")
	}
}

func TestState_MissingFileIsEmpty(t *testing.T) {
	s := LoadState(filepath.Join(t.TempDir(), "absent.json"))
	if got := s.Get("anything"); got.LastMatched || !got.LastFired.IsZero() {
		t.Errorf("Get() on empty state = %+v, want the zero value", got)
	}
}

// TestState_CorruptFileIsEmpty covers a truncated or hand-edited state file:
// losing the history is not a reason to refuse to notify.
func TestState_CorruptFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify_state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	s := LoadState(path)
	if got := s.Get("anything"); got.LastMatched {
		t.Errorf("Get() = %+v, want the zero value", got)
	}
	// And it should still be writable afterwards.
	s.Set("r", RuleState{LastMatched: true})
	if err := s.Save(); err != nil {
		t.Errorf("Save() error = %v", err)
	}
}

func TestState_SaveCreatesDirAndUsesTightPerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "notify_state.json")

	s := LoadState(path)
	s.Set("r", RuleState{LastMatched: true})
	if err := s.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("state file mode = %o, want 600", perm)
	}
}

func TestState_EmptyPathIsNoOp(t *testing.T) {
	s := LoadState("")
	s.Set("r", RuleState{LastMatched: true})
	if err := s.Save(); err != nil {
		t.Errorf("Save() with no path should be a no-op, got %v", err)
	}
	if !s.Get("r").LastMatched {
		t.Error("in-memory state should still work without a path")
	}
}

// TestState_ReloadPicksUpAnotherProcess covers the tray and a `growud serve`
// instance sharing one state file: the second must see the first's fires
// rather than notifying all over again from a stale in-memory copy.
func TestState_ReloadPicksUpAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify_state.json")
	fired := time.Date(2026, 8, 25, 14, 30, 0, 0, time.UTC)

	first := LoadState(path)
	second := LoadState(path)

	first.Set("ev_charge_window", RuleState{LastFired: fired, LastMatched: true})
	if err := first.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Without the reload the second process still believes the rule is unfired.
	if second.Get("ev_charge_window").LastMatched {
		t.Fatal("precondition: the second state should start out unaware")
	}

	second.Reload()
	if got := second.Get("ev_charge_window"); !got.LastMatched || !got.LastFired.Equal(fired) {
		t.Errorf("after Reload() = %+v, want the first process's write", got)
	}
}

// TestState_ReloadKeepsOwnWrites makes sure Reload never discards changes this
// process just made — it must only pick up somebody else's.
func TestState_ReloadKeepsOwnWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify_state.json")

	s := LoadState(path)
	s.Set("r", RuleState{LastMatched: true})
	if err := s.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	s.Reload()
	if !s.Get("r").LastMatched {
		t.Error("Reload() dropped this process's own write")
	}

	// An unsaved in-memory change also survives, because nothing else has
	// touched the file.
	s.Set("r2", RuleState{LastMatched: true})
	s.Reload()
	if !s.Get("r2").LastMatched {
		t.Error("Reload() dropped an unsaved in-memory change")
	}
}

func TestState_ReloadWithNoFileIsHarmless(t *testing.T) {
	s := LoadState(filepath.Join(t.TempDir(), "absent.json"))
	s.Set("r", RuleState{LastMatched: true})

	s.Reload()
	if !s.Get("r").LastMatched {
		t.Error("Reload() against a missing file should leave memory alone")
	}

	// And with no path configured at all.
	LoadState("").Reload()
}
