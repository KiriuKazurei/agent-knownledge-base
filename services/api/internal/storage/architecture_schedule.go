package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// scheduleInitialNext normalizes a configured anchor into the first run at or
// after creation. Anchors are persisted so restart recovery is deterministic.
func scheduleInitialNext(kind, anchor string, now time.Time) time.Time {
	now = now.UTC()
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(anchor))
	if err != nil {
		parsed = now
	}
	parsed = parsed.UTC()
	switch kind {
	case "daily":
		for parsed.Before(now) {
			parsed = parsed.Add(24 * time.Hour)
		}
	case "weekly":
		for parsed.Before(now) {
			parsed = parsed.Add(7 * 24 * time.Hour)
		}
	case "interval":
		if parsed.Before(now) {
			parsed = now
		}
	default:
		return time.Time{}
	}
	return parsed
}

func scheduleNext(kind string, interval int, due, now time.Time) time.Time {
	due, now = due.UTC(), now.UTC()
	switch kind {
	case "daily":
		for !due.After(now) {
			due = due.Add(24 * time.Hour)
		}
	case "weekly":
		for !due.After(now) {
			due = due.Add(7 * 24 * time.Hour)
		}
	case "interval":
		period := time.Duration(interval) * time.Second
		if period < time.Minute {
			period = time.Minute
		}
		for !due.After(now) {
			due = due.Add(period)
		}
	default:
		return time.Time{}
	}
	return due
}

// DueArchitectureProject is the scheduler's minimal durable input. It keeps
// filesystem paths and project policy in the normal project lookup path.
type DueArchitectureProject struct {
	ProjectID string
	Schedule  string
	Interval  int
	NextAt    time.Time
}

func (s *Store) ListDueArchitectureProjects(ctx context.Context, now time.Time) ([]DueArchitectureProject, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,schedule_kind,scan_period_seconds,schedule_next_at FROM architecture_projects WHERE deleted_at IS NULL AND schedule_kind<>? AND schedule_next_at<>'' AND schedule_next_at<=? ORDER BY schedule_next_at,id`, "manual", now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DueArchitectureProject{}
	for rows.Next() {
		var item DueArchitectureProject
		var next string
		if err := rows.Scan(&item.ProjectID, &item.Schedule, &item.Interval, &next); err != nil {
			return nil, err
		}
		item.NextAt = parseTime(next)
		items = append(items, item)
	}
	return items, rows.Err()
}

// MarkArchitectureSchedule advances a due schedule only after its task was
// successfully enqueued. Leaving it unchanged while a project is active
// provides a single catch-up run after the active task finishes.
func (s *Store) MarkArchitectureSchedule(ctx context.Context, projectID string, due time.Time, now time.Time) error {
	var kind string
	var interval int
	if err := s.DB.QueryRowContext(ctx, `SELECT schedule_kind,scan_period_seconds FROM architecture_projects WHERE id=? AND deleted_at IS NULL`, projectID).Scan(&kind, &interval); err != nil {
		return err
	}
	next := scheduleNext(kind, interval, due, now)
	if next.IsZero() {
		return errors.New("manual architecture schedules cannot be advanced")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE architecture_projects SET schedule_next_at=?,updated_at=? WHERE id=? AND schedule_kind<>? AND schedule_next_at=?`, next.Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), projectID, "manual", due.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
