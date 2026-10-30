package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (tx *Tx) UpsertExclude(ctx context.Context, policy ExcludePolicy) (ExcludePolicy, error) {
	existing, found, err := tx.findExcludeForUpsert(ctx, policy.TrafficID, policy.Email)
	if err != nil {
		return ExcludePolicy{}, err
	}
	note := strings.TrimSpace(policy.Note)
	if !found {
		id, err := tx.insertReturningID(ctx, `
			INSERT INTO xui_factor_excludes(
				traffic_id, inbound_id, email, state, note,
				created_at, updated_at, activated_at, disabled_at
			)
			VALUES(?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, NULL)
		`, policy.TrafficID, policy.InboundID, policy.Email, StateActive, note, policy.CreatedAt, policy.UpdatedAt, policy.ActivatedAt)
		if err != nil {
			return ExcludePolicy{}, err
		}
		return tx.FindExcludeByID(ctx, id)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_excludes
		SET traffic_id = ?, inbound_id = ?, state = ?, note = NULLIF(?, ''),
			updated_at = ?, activated_at = ?, disabled_at = NULL
		WHERE id = ?
	`, policy.TrafficID, policy.InboundID, StateActive, note, policy.UpdatedAt, policy.ActivatedAt, existing.ID); err != nil {
		return ExcludePolicy{}, err
	}
	return tx.FindExcludeByID(ctx, existing.ID)
}

// RelinkExcludeRow moves an active exclude onto the given traffic row. If
// another row already holds the target (traffic, inbound, email) tuple, an
// active holder keeps the policy and this row is disabled; an inactive holder
// is reactivated with this row's note instead.
func (tx *Tx) RelinkExcludeRow(ctx context.Context, policy ExcludePolicy, current ClientTraffic, now int64) error {
	var occupantID int64
	var occupantState string
	err := tx.QueryRowContext(ctx, `
		SELECT id, state
		FROM xui_factor_excludes
		WHERE traffic_id = ? AND inbound_id = ? AND email = ? AND id <> ?
		ORDER BY id DESC
		LIMIT 1
	`, current.ID, current.InboundID, policy.Email, policy.ID).Scan(&occupantID, &occupantState)
	if errors.Is(err, sql.ErrNoRows) {
		_, err := tx.ExecContext(ctx, `
			UPDATE xui_factor_excludes
			SET traffic_id = ?, inbound_id = ?, updated_at = ?
			WHERE id = ?
		`, current.ID, current.InboundID, now, policy.ID)
		return err
	}
	if err != nil {
		return err
	}
	if occupantState != StateActive {
		if _, err := tx.ExecContext(ctx, `
			UPDATE xui_factor_excludes
			SET state = ?, note = ?, activated_at = ?, updated_at = ?
			WHERE id = ?
		`, StateActive, policy.Note, policy.ActivatedAt, now, occupantID); err != nil {
			return err
		}
	}
	return tx.DisableExclude(ctx, policy.ID, now)
}

// findExcludeForUpsert prefers the active row, then the newest row for this
// traffic row, then the newest row for the email. Only the last case can point
// at a replaced traffic row, and then no row exists for the current one.
func (tx *Tx) findExcludeForUpsert(ctx context.Context, trafficID int64, email string) (ExcludePolicy, bool, error) {
	existing, err := tx.FindActiveExcludeForClient(ctx, trafficID, email)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ExcludePolicy{}, false, err
	}
	existing, err = tx.FindExcludeForClient(ctx, trafficID, email)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ExcludePolicy{}, false, err
	}
	existing, err = tx.FindExcludeByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return ExcludePolicy{}, false, nil
	}
	if err != nil {
		return ExcludePolicy{}, false, err
	}
	return existing, true, nil
}

func (tx *Tx) DisableExclude(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_excludes
		SET state = ?, updated_at = ?, disabled_at = ?
		WHERE id = ?
	`, StateDisabled, now, now, id)
	return err
}

func (tx *Tx) FindActiveExcludeForClient(ctx context.Context, trafficID int64, email string) (ExcludePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes
		WHERE traffic_id = ? AND email = ? AND state = ?
		ORDER BY id DESC
		LIMIT 1
	`, trafficID, email, StateActive)
	return scanExclude(row)
}

func (tx *Tx) FindExcludeByID(ctx context.Context, id int64) (ExcludePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes
		WHERE id = ?
		LIMIT 1
	`, id)
	return scanExclude(row)
}

func (tx *Tx) FindExcludeForClient(ctx context.Context, trafficID int64, email string) (ExcludePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes
		WHERE traffic_id = ? AND email = ?
		ORDER BY id DESC
		LIMIT 1
	`, trafficID, email)
	return scanExclude(row)
}

func (tx *Tx) FindExcludeByEmail(ctx context.Context, email string) (ExcludePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes
		WHERE email = ?
		ORDER BY id DESC
		LIMIT 1
	`, email)
	return scanExclude(row)
}

func (tx *Tx) FindActiveExcludeByEmail(ctx context.Context, email string) (ExcludePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_excludes
		WHERE email = ? AND state = ?
		ORDER BY id DESC
		LIMIT 1
	`, email, StateActive)
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
		SELECT ex.id, ex.traffic_id, ex.inbound_id, ex.email, ex.state, COALESCE(ex.note, ''),
			ex.created_at, ex.updated_at, ex.activated_at, ex.disabled_at
		FROM xui_factor_excludes ex`
	where := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if inboundID != nil {
		// Policies whose traffic row is gone fall back to the stored inbound.
		query += `
		LEFT JOIN client_traffics ct ON ct.id = ex.traffic_id AND ct.email = ex.email`
	}
	if !includeInactive {
		where = append(where, "ex.state = ?")
		args = append(args, StateActive)
	}
	if inboundID != nil {
		where = append(where, "("+clientInboundMembership("COALESCE(ct.email, ex.email)")+" OR (ct.id IS NULL AND ex.inbound_id = ?))")
		args = append(args, *inboundID, *inboundID)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY ex.inbound_id, ex.email, ex.traffic_id"

	rows, err := s.queryContext(ctx, query, args...)
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

	rows, err := s.queryContext(ctx, `
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
