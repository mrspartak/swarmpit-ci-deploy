package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type WatchOptions struct {
	Timeout  time.Duration // give up after this long
	Settle   time.Duration // all replicas must stay running this long after the update completes
	Interval time.Duration // poll period
}

// Watch blocks until the redeploy of service id (started at `since`, when its
// version was `prevVersion`) converges. It returns nil on success and a
// descriptive error on rollback, pause, crash loop or timeout.
func Watch(ctx context.Context, sp *Swarmpit, id string, prevVersion int64, since time.Time, o WatchOptions) error {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	var settledAt time.Time
	var last Service
	for {
		svc, err := sp.Service(ctx, id)
		if err == nil {
			last = svc
			switch svc.Status.Update {
			case "paused", "rollback_started", "rollback_paused", "rollback_completed":
				return fmt.Errorf("update %s: %s%s", svc.Status.Update, svc.Status.Message, taskErrors(ctx, sp, id, since))
			case "completed":
				if svc.Version <= prevVersion {
					break // status is left over from a previous update
				}
				ok := svc.Status.Tasks.Running >= svc.Status.Tasks.Total && svc.Status.Tasks.Total > 0
				switch {
				case !ok:
					settledAt = time.Time{}
				case settledAt.IsZero():
					settledAt = time.Now()
				case time.Since(settledAt) >= o.Settle:
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("timeout after %s: update=%q running=%d/%d%s", o.Timeout, last.Status.Update,
					last.Status.Tasks.Running, last.Status.Tasks.Total, taskErrors(context.Background(), sp, id, since))
			}
			return ctx.Err()
		case <-time.After(o.Interval):
		}
	}
}

// taskErrors summarises failed tasks created since the redeploy started.
func taskErrors(ctx context.Context, sp *Swarmpit, id string, since time.Time) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tasks, err := sp.Tasks(ctx, id)
	if err != nil {
		return ""
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range tasks {
		if t.CreatedAt.Before(since) || (t.State != "failed" && t.State != "rejected") {
			continue
		}
		msg := t.State
		if t.Status.Error != "" {
			msg += ": " + t.Status.Error
		}
		if !seen[msg] {
			seen[msg] = true
			out = append(out, msg)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return " | tasks: " + strings.Join(out, "; ")
}
