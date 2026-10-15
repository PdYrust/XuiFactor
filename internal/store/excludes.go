package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (tx *Tx) UpsertExclude(ctx context.Context, policy ExcludePolicy) (ExcludePolicy, error) {
	existing, created, err := tx.findExcludeForUpsert(ctx, policy.TrafficID, policy.Email)
	if err != nil {
		return ExcludePolicy{}, err
	}
	if created {
		_, err = tx.ExecContext(ctx, `
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
		return tx.FindExcludeForClient(ctx, policy.TrafficID, policy.Email)
	}

	if existing.TrafficID != policy.TrafficID {
		// The stored row points at a replaced traffic row; move it onto the
		// current row for the same client instead of inserting a duplicate.
		moved, err := tx.relinkExclude(ctx, existing.ID, policy)
		if err != nil {
			return ExcludePolicy{}, err
		}
		if moved {
			return tx.FindExcludeByID(ctx, existing.ID)
		}
		// Another row already targets the current traffic row; retire the stale
		// one and update the current row instead.
		if err := tx.DisableExclude(ctx, existing.ID, policy.UpdatedAt); err != nil {
			return ExcludePolicy{}, err
		}
		current, err := tx.FindExcludeForClient(ctx, policy.TrafficID, policy.Email)
		if err != nil {
			return ExcludePolicy{}, err
		}
		if err := tx.updateExcludeFields(ctx, current.ID, policy); err != nil {
			return ExcludePolicy{}, err
		}
		return tx.FindExcludeByID(ctx, current.ID)
	}

	if err := tx.updateExcludeFields(ctx, existing.ID, policy); err != nil {
		return ExcludePolicy{}, err
	}
	return tx.FindExcludeByID(ctx, existing.ID)
}

func (tx *Tx) updateExcludeFields(ctx context.Context, id int64, policy ExcludePolicy) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_excludes
		SET inbound_id = ?, state = ?, note = NULLIF(?, ''),
			updated_at = ?, activated_at = ?, disabled_at = NULL
		WHERE id = ?
	`, policy.InboundID, StateActive, strings.TrimSpace(policy.Note), policy.UpdatedAt, policy.ActivatedAt, id)
	return err
}

// relinkExclude moves a policy row onto the client's current traffic row. It
// reports false when another row already targets that traffic row.
func (tx *Tx) relinkExclude(ctx context.Context, id int64, policy ExcludePolicy) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_excludes
		SET traffic_id = ?, inbound_id = ?, state = ?, note = NULLIF(?, ''),
			updated_at = ?, activated_at = ?, disabled_at = NULL
		WHERE id = ?
			AND NOT EXISTS (
				SELECT 1 FROM xui_factor_excludes other
				WHERE other.email = ? AND other.traffic_id = ? AND other.id <> ?
			)
	`, policy.TrafficID, policy.InboundID, StateActive, strings.TrimSpace(policy.Note), policy.UpdatedAt, policy.ActivatedAt, id, policy.Email, policy.TrafficID, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// RelinkExcludeRow moves an active exclude onto the given traffic row. When
// another row already occupies the target (traffic row, inbound, email) tuple,
// an active occupant keeps the policy and this row is retired, while an
// inactive occupant is reactivated with this row's note in its place.
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

// findExcludeForUpsert prefers an active row so reactivation can never leave
// two active rows for one client; without one it falls back to the newest row,
// including a stale row that still points at a replaced traffic row.
func (tx *Tx) findExcludeForUpsert(ctx context.Context, trafficID int64, email string) (ExcludePolicy, bool, error) {
	existing, err := tx.FindActiveExcludeForClient(ctx, trafficID, email)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ExcludePolicy{}, false, err
	}
	existing, err = tx.FindExcludeForClient(ctx, trafficID, email)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ExcludePolicy{}, false, err
	}
	existing, err = tx.FindExcludeByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return ExcludePolicy{}, true, nil
	}
	if err != nil {
		return ExcludePolicy{}, false, err
	}
	return existing, false, nil
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

// FindExcludeByEmail reports the newest exclude row for a client regardless of
// the stored traffic row, which may point at a replaced row after a 3.x panel
// upgrade or client rename.
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
		// Resolve the inbound through the authoritative clients/client_inbounds
		// link; fall back to the stored pointer only for clients whose traffic
		// row is gone.
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
