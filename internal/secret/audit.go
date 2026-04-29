package secret

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

// NewSlogAuditor returns a slog-backed Auditor when enabled. Disabled audit
// logging intentionally falls back to the no-op boundary.
func NewSlogAuditor(enabled bool) Auditor {
	return NewSlogAuditorWithLogger(enabled, slog.Default())
}

// NewSlogAuditorWithLogger returns a slog-backed Auditor using logger. Tests
// can pass a buffer-backed logger to assert the emitted fields.
func NewSlogAuditorWithLogger(enabled bool, logger *slog.Logger) Auditor {
	if !enabled {
		return NoopAuditor{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return SlogAuditor{logger: logger}
}

// SlogAuditor writes non-sensitive secret audit events through slog.
type SlogAuditor struct {
	logger *slog.Logger
}

// Record implements Auditor.
func (a SlogAuditor) Record(ctx context.Context, event AuditEvent) error {
	if a.logger == nil {
		a.logger = slog.Default()
	}
	at := event.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	secretIDs := append([]string(nil), event.SecretIDs...)
	sort.Strings(secretIDs)

	a.logger.InfoContext(ctx, "secret audit event",
		"at", at.Format(time.RFC3339Nano),
		"action", event.Action,
		"result", event.Result,
		"org", event.Org,
		"project", event.Project,
		"service", event.Service,
		"secret_ids", secretIDs,
	)
	return nil
}
