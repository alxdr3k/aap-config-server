package agent

import (
	"testing"
	"time"
)

func TestDebouncerAppliesFirstChangeImmediately(t *testing.T) {
	debouncer := newTestDebouncer(t)
	start := debounceStartTime()

	decision := debouncer.RecordChange(start)
	if !decision.ApplyNow || decision.Reason != DebounceReasonImmediate {
		t.Fatalf("first decision: %+v", decision)
	}
	if debouncer.Pending() {
		t.Fatal("first immediate apply should not leave pending debounce")
	}
}

func TestDebouncerBatchesCooldownChangesUntilQuietPeriod(t *testing.T) {
	debouncer := newTestDebouncer(t)
	start := debounceStartTime()
	if !debouncer.RecordChange(start).ApplyNow {
		t.Fatal("first change should apply immediately")
	}

	second := debouncer.RecordChange(start.Add(5 * time.Second))
	if second.ApplyNow || second.NextAt != start.Add(15*time.Second) || second.Reason != DebounceReasonCooling {
		t.Fatalf("second decision: %+v", second)
	}
	third := debouncer.RecordChange(start.Add(12 * time.Second))
	if third.ApplyNow || third.NextAt != start.Add(22*time.Second) {
		t.Fatalf("third decision: %+v", third)
	}
	if due := debouncer.Due(start.Add(21 * time.Second)); due.ApplyNow || due.NextAt != start.Add(22*time.Second) {
		t.Fatalf("early due decision: %+v", due)
	}
	due := debouncer.Due(start.Add(22 * time.Second))
	if !due.ApplyNow || due.Reason != DebounceReasonQuiet {
		t.Fatalf("quiet due decision: %+v", due)
	}
	if debouncer.Pending() {
		t.Fatal("quiet apply should clear pending debounce")
	}
}

func TestDebouncerForcesApplyAtMaxWait(t *testing.T) {
	debouncer := newTestDebouncer(t)
	start := debounceStartTime()
	if !debouncer.RecordChange(start).ApplyNow {
		t.Fatal("first change should apply immediately")
	}

	for _, offset := range []time.Duration{5, 14, 23, 32, 41, 50, 59, 68, 77, 86, 95, 104, 113, 122} {
		decision := debouncer.RecordChange(start.Add(offset * time.Second))
		if decision.ApplyNow {
			t.Fatalf("change at %s should not apply immediately: %+v", offset, decision)
		}
	}
	if due := debouncer.Due(start.Add(124 * time.Second)); due.ApplyNow {
		t.Fatalf("max wait should not apply early: %+v", due)
	}
	due := debouncer.Due(start.Add(125 * time.Second))
	if !due.ApplyNow || due.Reason != DebounceReasonMaxWait {
		t.Fatalf("max wait due decision: %+v", due)
	}
}

func TestDebouncerDoesNotDebounceAfterCooldown(t *testing.T) {
	debouncer := newTestDebouncer(t)
	start := debounceStartTime()
	if !debouncer.RecordChange(start).ApplyNow {
		t.Fatal("first change should apply immediately")
	}

	decision := debouncer.RecordChange(start.Add(11 * time.Second))
	if !decision.ApplyNow || decision.Reason != DebounceReasonImmediate {
		t.Fatalf("post-cooldown decision: %+v", decision)
	}
}

func TestDebouncerUsesCooldownAfterDebouncedApply(t *testing.T) {
	debouncer := newTestDebouncer(t)
	start := debounceStartTime()
	debouncer.RecordChange(start)
	debouncer.RecordChange(start.Add(5 * time.Second))
	if due := debouncer.Due(start.Add(15 * time.Second)); !due.ApplyNow {
		t.Fatalf("quiet period should apply: %+v", due)
	}

	decision := debouncer.RecordChange(start.Add(20 * time.Second))
	if decision.ApplyNow || decision.NextAt != start.Add(30*time.Second) {
		t.Fatalf("change during post-apply cooldown should be debounced: %+v", decision)
	}
}

func TestDebouncerRecordChangeHandlesOverduePendingChange(t *testing.T) {
	debouncer := newTestDebouncer(t)
	start := debounceStartTime()
	debouncer.RecordChange(start)
	debouncer.RecordChange(start.Add(5 * time.Second))

	decision := debouncer.RecordChange(start.Add(30 * time.Second))
	if !decision.ApplyNow || decision.Reason != DebounceReasonQuiet || decision.NextAt != start.Add(40*time.Second) {
		t.Fatalf("overdue change decision: %+v", decision)
	}
	if !debouncer.Pending() {
		t.Fatal("new change should remain pending after applying overdue change")
	}
	due := debouncer.Due(start.Add(40 * time.Second))
	if !due.ApplyNow || due.Reason != DebounceReasonQuiet {
		t.Fatalf("new pending change should apply after quiet period: %+v", due)
	}
}

func TestDebouncerValidation(t *testing.T) {
	tests := []DebounceConfig{
		{QuietPeriod: time.Second, MaxWait: time.Second},
		{Cooldown: time.Second, MaxWait: time.Second},
		{Cooldown: time.Second, QuietPeriod: time.Second},
		{Cooldown: time.Second, QuietPeriod: 2 * time.Second, MaxWait: time.Second},
	}
	for _, cfg := range tests {
		if _, err := NewDebouncer(cfg); err == nil {
			t.Fatalf("NewDebouncer(%+v): expected validation error", cfg)
		}
	}
}

func newTestDebouncer(t *testing.T) *Debouncer {
	t.Helper()
	debouncer, err := NewDebouncer(DebounceConfig{
		Cooldown:    10 * time.Second,
		QuietPeriod: 10 * time.Second,
		MaxWait:     120 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewDebouncer: %v", err)
	}
	return debouncer
}

func debounceStartTime() time.Time {
	return time.Date(2026, 4, 30, 4, 30, 0, 0, time.UTC)
}
