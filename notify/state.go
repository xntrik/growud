package notify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RuleState is what the engine remembers about a rule between evaluations.
type RuleState struct {
	LastFired time.Time `json:"last_fired,omitzero"`

	// LastMatched is the previous evaluation's result. It is what makes a rule
	// fire on the transition into its condition rather than on every poll.
	LastMatched bool `json:"last_matched"`
}

// State persists per-rule firing history so cooldowns and edge-triggering
// survive a restart of the app.
//
// The file is also the handover point between processes: the tray and a
// `growud serve` instance can both be evaluating the same rules, so State
// re-reads the file whenever another process has written it.
type State struct {
	path string
	mu   sync.Mutex
	data stateFile

	// stamp identifies the file contents last read or written, so Reload can
	// tell somebody else's write from our own.
	stamp fileStamp
}

// fileStamp is a cheap identity for the state file: a full re-read on every
// pass would be correct too, but this keeps the common case to one stat.
type fileStamp struct {
	modTime time.Time
	size    int64
}

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size()}
}

type stateFile struct {
	Rules map[string]RuleState `json:"rules"`
}

// LoadState reads the state file at path. A missing or unreadable file yields
// empty state rather than an error — losing history is not worth refusing to
// send notifications over.
func LoadState(path string) *State {
	s := &State{
		path: path,
		data: stateFile{Rules: map[string]RuleState{}},
	}

	s.read()
	return s
}

// Reload picks up a state file written by another process. It is a no-op when
// the file has not changed since this State last read or wrote it.
func (s *State) Reload() {
	if s.path == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if stampOf(s.path) == s.stamp {
		return
	}
	s.read()
}

// read replaces the in-memory rules from disk. A missing or unparseable file
// leaves what is already in memory alone. Callers hold the lock, except
// LoadState which has not published the State yet.
func (s *State) read() {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var parsed stateFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return
	}
	if parsed.Rules != nil {
		s.data.Rules = parsed.Rules
	}
	s.stamp = stampOf(s.path)
}

// Get returns the stored state for a rule.
func (s *State) Get(rule string) RuleState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Rules[rule]
}

// Set records new state for a rule.
func (s *State) Set(rule string, rs RuleState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Rules[rule] = rs
}

// Save writes the state file, creating the parent directory if needed. It is
// written 0600 since it records when you were notified about what.
func (s *State) Save() error {
	if s.path == "" {
		return nil
	}

	s.mu.Lock()
	raw, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("encoding notification state: %w", err)
	}

	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating state dir: %w", err)
		}
	}

	// Write to a temp file and rename so a crash mid-write can't leave a
	// truncated state file behind.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return fmt.Errorf("writing notification state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replacing notification state: %w", err)
	}

	// Record what we just wrote, so Reload does not read our own write back.
	s.mu.Lock()
	s.stamp = stampOf(s.path)
	s.mu.Unlock()

	return nil
}
