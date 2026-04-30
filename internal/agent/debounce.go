package agent

import (
	"errors"
	"fmt"
	"time"
)

// DebounceConfig controls leading-edge rollout debounce behavior.
type DebounceConfig struct {
	Cooldown    time.Duration
	QuietPeriod time.Duration
	MaxWait     time.Duration
}

// Debouncer tracks leading-edge debounce state for rollout triggers.
type Debouncer struct {
	cfg           DebounceConfig
	cooldownUntil time.Time
	pending       bool
	debounceStart time.Time
	lastChange    time.Time
}

// DebounceDecision is returned after a change event or timer check. ApplyNow
// and NextAt can both be set when a late change first flushes an overdue
// pending rollout and then records the new change as pending.
type DebounceDecision struct {
	ApplyNow bool
	NextAt   time.Time
	Reason   string
}

const (
	DebounceReasonImmediate = "immediate"
	DebounceReasonCooling   = "cooling"
	DebounceReasonPending   = "pending"
	DebounceReasonQuiet     = "quiet_period"
	DebounceReasonMaxWait   = "max_wait"
	DebounceReasonIdle      = "idle"
)

// NewDebouncer creates a leading-edge debounce state machine.
func NewDebouncer(cfg DebounceConfig) (*Debouncer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Debouncer{cfg: cfg}, nil
}

// Validate checks debounce timing constraints.
func (c DebounceConfig) Validate() error {
	if c.Cooldown <= 0 {
		return fmt.Errorf("debounce cooldown must be > 0, got %s", c.Cooldown)
	}
	if c.QuietPeriod <= 0 {
		return fmt.Errorf("debounce quiet period must be > 0, got %s", c.QuietPeriod)
	}
	if c.MaxWait <= 0 {
		return fmt.Errorf("debounce max wait must be > 0, got %s", c.MaxWait)
	}
	if c.MaxWait < c.QuietPeriod {
		return errors.New("debounce max wait must be >= quiet period")
	}
	return nil
}

// RecordChange records that a new config change needs rollout consideration.
// The first change after cooldown applies immediately; changes during cooldown
// or an active debounce window are batched until Due reports an apply.
func (d *Debouncer) RecordChange(at time.Time) DebounceDecision {
	if d == nil {
		return DebounceDecision{Reason: DebounceReasonIdle}
	}
	if d.pending {
		if due := d.Due(at); due.ApplyNow {
			d.pending = true
			d.debounceStart = at
			d.lastChange = at
			return DebounceDecision{ApplyNow: true, NextAt: d.nextDueAt(), Reason: due.Reason}
		}
	}
	if !d.pending && !d.inCooldown(at) {
		d.cooldownUntil = at.Add(d.cfg.Cooldown)
		return DebounceDecision{ApplyNow: true, Reason: DebounceReasonImmediate}
	}
	if !d.pending {
		d.pending = true
		d.debounceStart = at
	} else if at.Before(d.debounceStart) {
		d.debounceStart = at
	}
	d.lastChange = at
	return DebounceDecision{NextAt: d.nextDueAt(), Reason: DebounceReasonCooling}
}

// Due checks whether a pending debounced rollout should apply at the provided
// time. Callers can use NextAt to set their next timer.
func (d *Debouncer) Due(at time.Time) DebounceDecision {
	if d == nil || !d.pending {
		return DebounceDecision{Reason: DebounceReasonIdle}
	}
	quietDue := d.lastChange.Add(d.cfg.QuietPeriod)
	maxDue := d.debounceStart.Add(d.cfg.MaxWait)
	next := minTime(quietDue, maxDue)
	if at.Before(next) {
		return DebounceDecision{NextAt: next, Reason: DebounceReasonPending}
	}

	reason := DebounceReasonQuiet
	if next.Equal(maxDue) {
		reason = DebounceReasonMaxWait
	}
	d.pending = false
	d.debounceStart = time.Time{}
	d.lastChange = time.Time{}
	d.cooldownUntil = at.Add(d.cfg.Cooldown)
	return DebounceDecision{ApplyNow: true, Reason: reason}
}

// Pending reports whether there is a debounced rollout waiting to apply.
func (d *Debouncer) Pending() bool {
	return d != nil && d.pending
}

func (d *Debouncer) inCooldown(at time.Time) bool {
	return !d.cooldownUntil.IsZero() && at.Before(d.cooldownUntil)
}

func (d *Debouncer) nextDueAt() time.Time {
	if d == nil || !d.pending {
		return time.Time{}
	}
	return minTime(d.lastChange.Add(d.cfg.QuietPeriod), d.debounceStart.Add(d.cfg.MaxWait))
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
