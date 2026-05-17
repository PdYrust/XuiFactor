package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (tx *Tx) UpsertExclude(ctx context.Context, policy ExcludePolicy) (ExcludePolicy, error) {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO xui_factor_excludes(
			traffic_id, inbound_id, email, state, note,
			created_at, updated_at, activated_at, disabled_at
		)
		VALUES(?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, NULL)
		ON CONFLICT(traffic_id, inbound_id, email) DO UPDATE SET
			state = excluded.state,
			note = excluded.note,
			updated_at = excluded.updated_at,
			activated_at = excluded.activated_at,
			disabled_at = NULL
	`, policy.TrafficID, policy.InboundID, policy.Email, StateActive, strings.TrimSpace(policy.Note), policy.CreatedAt, policy.UpdatedAt, policy.ActivatedAt)
	if err != nil {
		return ExcludePolicy{}, err
	}
	return tx.FindExcludeByIdentity(ctx, policy.TrafficID, policy.InboundID, policy.Email)
}

func (tx *Tx) DisableExclude(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_excludes
		SET state = ?, updated_at = ?, disabled_at = ?
		WHERE id = ?
	`, StateDisabled, now, now, id)
	return err
}

func (tx *Tx) FindActiveExcludeForClient(ctx context.Context, trafficID, inboundID int64, email string) (ExcludePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes
		WHERE traffic_id = ? AND inbound_id = ? AND email = ? AND state = ?
		LIMIT 1
	`, trafficID, inboundID, email, StateActive)
	return scanExclude(row)
}

func (tx *Tx) FindExcludeByIdentity(ctx context.Context, trafficID, inboundID int64, email string) (ExcludePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes
		WHERE traffic_id = ? AND inbound_id = ? AND email = ?
		LIMIT 1
	`, trafficID, inboundID, email)
	return scanExclude(row)
}

func (tx *Tx) ListActiveExcludes(ctx context.Context) ([]ExcludePolicy, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes
		WHERE state = ?
		ORDER BY inbound_id, email, traffic_id
	`, StateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExcludeRows(rows)
}

func (s *Store) ListExcludes(ctx context.Context, includeInactive bool, inboundID *int64) ([]ExcludePolicy, error) {
	exists, err := s.tableExists(ctx, "xui_factor_excludes")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	query := `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes`
	where := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if !includeInactive {
		where = append(where, "state = ?")
		args = append(args, StateActive)
	}
	if inboundID != nil {
		where = append(where, "inbound_id = ?")
		args = append(args, *inboundID)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY inbound_id, email, traffic_id"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExcludeRows(rows)
}

func (s *Store) CountExcludes(ctx context.Context) (ExcludeCounts, error) {
	exists, err := s.tableExists(ctx, "xui_factor_excludes")
	if err != nil {
		return ExcludeCounts{}, err
	}
	if !exists {
		return ExcludeCounts{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT state, COUNT(*)
		FROM xui_factor_excludes
		GROUP BY state
	`)
	if err != nil {
		return ExcludeCounts{}, err
	}
	defer rows.Close()

	var counts ExcludeCounts
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return ExcludeCounts{}, err
		}
		if state == StateActive {
			counts.Active += count
		} else {
			counts.Inactive += count
		}
	}
	if err := rows.Err(); err != nil {
		return ExcludeCounts{}, err
	}
	return counts, nil
}

func scanExclude(row *sql.Row) (ExcludePolicy, error) {
	var policy ExcludePolicy
	var activatedAt, disabledAt sql.NullInt64
	err := row.Scan(
		&policy.ID,
		&policy.TrafficID,
		&policy.InboundID,
		&policy.Email,
		&policy.State,
		&policy.Note,
		&policy.CreatedAt,
		&policy.UpdatedAt,
		&activatedAt,
		&disabledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ExcludePolicy{}, ErrNotFound
	}
	if err != nil {
		return ExcludePolicy{}, err
	}
	policy.ActivatedAt = nullablePtr(activatedAt)
	policy.DisabledAt = nullablePtr(disabledAt)
	return policy, nil
}

func scanExcludeRows(rows *sql.Rows) ([]ExcludePolicy, error) {
	var policies []ExcludePolicy
	for rows.Next() {
		var policy ExcludePolicy
		var activatedAt, disabledAt sql.NullInt64
		if err := rows.Scan(
			&policy.ID,
			&policy.TrafficID,
			&policy.InboundID,
			&policy.Email,
			&policy.State,
			&policy.Note,
			&policy.CreatedAt,
			&policy.UpdatedAt,
			&activatedAt,
			&disabledAt,
		); err != nil {
			return nil, err
		}
		policy.ActivatedAt = nullablePtr(activatedAt)
		policy.DisabledAt = nullablePtr(disabledAt)
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return policies, nil
}
