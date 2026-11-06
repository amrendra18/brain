package index

import (
	"brain/internal/platform"
	"brain/internal/redact"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func privacyRetryFixture(t *testing.T) (string, *redact.Policy, *platform.Lock) {
	t.Helper()
	source, state, db, config := fixture(t)
	write(t, source, "README.md", "# Keeper\n\nStored keeper with PrivateRetryValue.")
	refresh(t, db, state, config, false, 0)
	saved := map[string][]byte{}
	for _, name := range []string{"index.sqlite", "index.sqlite-wal"} {
		data, err := os.ReadFile(filepath.Join(state, name))
		if err != nil || len(data) == 0 {
			t.Fatal("fixture needs committed database and WAL bytes", name, err)
		}
		saved[name] = data
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Restore committed pages without shared memory, after closing the writer.
	for name, data := range saved {
		if err := os.WriteFile(filepath.Join(state, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !orphanWAL(state) {
		t.Fatal("fixture did not enter the orphan WAL branch")
	}
	t.Cleanup(func() {
		for name, before := range saved {
			after, err := os.ReadFile(filepath.Join(state, name))
			if err != nil || !bytes.Equal(before, after) {
				t.Error("privacy read changed fixture bytes", name, err)
			}
		}
		if _, err := os.Stat(filepath.Join(state, "index.sqlite-shm")); !errors.Is(err, os.ErrNotExist) {
			t.Error("privacy read recreated shared memory", err)
		}
	})
	lock, err := platform.Acquire(state, "index", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	if opened, err := OpenRead(state, true); !errors.Is(err, platform.ErrLocked) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("fixture must first refuse the competing reader", err)
	}
	return state, config.Privacy(), lock
}

type privacyRetryResult struct {
	required bool
	err      error
}

func startPrivacyRetry(t *testing.T, state string, policy *redact.Policy) <-chan privacyRetryResult {
	t.Helper()
	result := make(chan privacyRetryResult, 1)
	go func() {
		required, err := PrivacyRequired(state, policy)
		result <- privacyRetryResult{required, err}
	}()
	select {
	case got := <-result:
		t.Fatal("privacy check returned before the lock could clear", got.required, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	return result
}

func finishPrivacyRetry(t *testing.T, result <-chan privacyRetryResult, required bool, want error) {
	t.Helper()
	select {
	case got := <-result:
		if got.required != required || !errors.Is(got.err, want) {
			t.Fatal("privacy retry result", got.required, got.err, "wanted", required, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("privacy retry did not react to the changed state")
	}
}

func TestPrivacyRequiredRetriesWriterClose(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "same-policy"
		if changed {
			name = "changed-policy"
		}
		t.Run(name, func(t *testing.T) {
			state, policy, lock := privacyRetryFixture(t)
			if changed {
				policy = privacyPolicy(t, "PrivateRetryValue")
			}
			started := time.Now()
			result := startPrivacyRetry(t, state, policy)
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			finishPrivacyRetry(t, result, changed, nil)
			t.Log("recovered after", time.Since(started))
			// Both the snapshot reader and its guard must be closed on return.
			next, err := platform.Acquire(state, "index", false)
			if err != nil {
				t.Fatal("privacy retry retained the index lock", err)
			}
			next.Close()
		})
	}
}

func TestPrivacyRequiredRetainedLock(t *testing.T) {
	state, policy, _ := privacyRetryFixture(t)
	started := time.Now()
	required, err := PrivacyRequired(state, policy)
	elapsed := time.Since(started)
	if required || !errors.Is(err, platform.ErrLocked) {
		t.Fatal("retained lock must refuse", required, err)
	}
	if elapsed < 5*time.Second || elapsed > 7*time.Second {
		t.Fatal("retained-lock retry scheduling exceeded its budget", elapsed)
	}
	t.Log("retained lock refused after", elapsed)
}

func TestPrivacyRequiredRechecksUnsafeState(t *testing.T) {
	for _, marker := range []bool{true, false} {
		name, data, want := privacyMarker, []byte("{}"), error(nil)
		if !marker {
			name, data, want = "schedule.json", []byte("invalid receipt"), ErrPrivacySchedule
		}
		t.Run(name, func(t *testing.T) {
			state, policy, _ := privacyRetryFixture(t)
			result := startPrivacyRetry(t, state, policy)
			if err := platform.WriteState(state, name, data); err != nil {
				t.Fatal(err)
			}
			finishPrivacyRetry(t, result, marker, want)
		})
	}
}

func TestPrivacyRequiredDoesNotRetryOtherErrors(t *testing.T) {
	state, policy, _ := privacyRetryFixture(t)
	if err := platform.WriteState(state, "schedule.json", []byte("invalid receipt")); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	required, err := PrivacyRequired(state, policy)
	if required || !errors.Is(err, ErrPrivacySchedule) || time.Since(started) > time.Second {
		t.Fatal("non-lock refusal must return promptly", required, err, time.Since(started))
	}
}
