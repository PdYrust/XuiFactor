package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (tx *Tx) UpsertOverride(ctx context.Context, policy OverridePolicy) (OverridePolicy, bool, error) {
	existing, created, err := tx.findOverrideForUpsert(ctx, policy.TrafficID, policy.Email)
	if err != nil {
		return OverridePolicy{}, false, err
	}

	if created {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO xui_factor_overrides(
				traffic_id, inbound_id, email, factor_ppm, state, note,
				created_at, updated_at, activated_at, disabled_at
			)
			VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, NULL)
			ON CONFLICT(traffic_id, inbound_id, email) DO UPDATE SET
				factor_ppm = excluded.factor_ppm,
				state = excluded.state,
				note = excluded.note,
				updated_at = excluded.updated_at,
				activated_at = excluded.activated_at,
				disabled_at = NULL
		`, policy.TrafficID, policy.InboundID, policy.Email, policy.FactorPPM, StateActive, strings.TrimSpace(policy.Note), policy.CreatedAt, policy.UpdatedAt, policy.ActivatedAt)
		if err != nil {
			return OverridePolicy{}, false, err
		}
		updated, err := tx.FindOverrideForClient(ctx, policy.TrafficID, policy.Email)
		if err != nil {
			return OverridePolicy{}, false, err
		}
		return updated, false, nil
	}

	changed := existing.State == StateActive &&
		(existing.FactorPPM != policy.FactorPPM || strings.TrimSpace(existing.Note) != strings.TrimSpace(policy.Note))

	if existing.TrafficID != policy.TrafficID {
		// The stored row points at a replaced traffic row; move it onto the
		// current row for the same client instead of inserting a duplicate.
		moved, err := tx.relinkOverride(ctx, existing.ID, policy)
		if err != nil {
			return OverridePolicy{}, false, err
		}
		if moved {
			updated, err := tx.FindOverrideByID(ctx, existing.ID)
			if err != nil {
				return OverridePolicy{}, false, err
			}
			return updated, changed, nil
		}
		// Another row already targets the current traffic row; retire the stale
		// one and update the current row instead.
		if err := tx.DisableOverride(ctx, existing.ID, policy.UpdatedAt); err != nil {
			return OverridePolicy{}, false, err
		}
		current, err := tx.FindOverrideForClient(ctx, policy.TrafficID, policy.Email)
		if err != nil {
			return OverridePolicy{}, false, err
		}
		if err := tx.updateOverrideFields(ctx, current.ID, policy); err != nil {
			return OverridePolicy{}, false, err
		}
		updated, err := tx.FindOverrideByID(ctx, current.ID)
		if err != nil {
			return OverridePolicy{}, false, err
		}
		return updated, changed, nil
	}

	if err := tx.updateOverrideFields(ctx, existing.ID, policy); err != nil {
		return OverridePolicy{}, false, err
	}
	updated, err := tx.FindOverrideByID(ctx, existing.ID)
	if err != nil {
		return OverridePolicy{}, false, err
	}
	return updated, changed, nil
}

func (tx *Tx) updateOverrideFields(ctx context.Context, id int64, policy OverridePolicy) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_overrides
		SET inbound_id = ?, factor_ppm = ?, state = ?, note = NULLIF(?, ''),
			updated_at = ?, activated_at = ?, disabled_at = NULL
		WHERE id = ?
	`, policy.InboundID, policy.FactorPPM, StateActive, strings.TrimSpace(policy.Note), policy.UpdatedAt, policy.ActivatedAt, id)
	return err
}

// relinkOverride moves a policy row onto the client's current traffic row. It
// reports false when another row already targets that traffic row.
func (tx *Tx) relinkOverride(ctx context.Context, id int64, policy OverridePolicy) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_overrides
		SET traffic_id = ?, inbound_id = ?, factor_ppm = ?, state = ?, note = NULLIF(?, ''),
			updated_at = ?, activated_at = ?, disabled_at = NULL
		WHERE id = ?
			AND NOT EXISTS (
				SELECT 1 FROM xui_factor_overrides other
				WHERE other.email = ? AND other.traffic_id = ? AND other.id <> ?
			)
	`, policy.TrafficID, policy.InboundID, policy.FactorPPM, StateActive, strings.TrimSpace(policy.Note), policy.UpdatedAt, policy.ActivatedAt, id, policy.Email, policy.TrafficID, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// RelinkOverrideRow moves an active override onto the given traffic row. When
// another row already occupies the target (traffic row, inbound, email) tuple,
// an active occupant keeps the policy and this row is retired, while an
// inactive occupant is reactivated with this row's factor in its place.
func (tx *Tx) RelinkOverrideRow(ctx context.Context, policy OverridePolicy, current ClientTraffic, now int64) error {
	var occupantID int64
	var occupantState string
	err := tx.QueryRowContext(ctx, `
		SELECT id, state
		FROM xui_factor_overrides
		WHERE traffic_id = ? AND inbound_id = ? AND email = ? AND id <> ?
		ORDER BY id DESC
		LIMIT 1
	`, current.ID, current.InboundID, policy.Email, policy.ID).Scan(&occupantID, &occupantState)
	if errors.Is(err, sql.ErrNoRows) {
		_, err := tx.ExecContext(ctx, `
			UPDATE xui_factor_overrides
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
			UPDATE xui_factor_overrides
			SET state = ?, factor_ppm = ?, note = ?, activated_at = ?, updated_at = ?
			WHERE id = ?
		`, StateActive, policy.FactorPPM, policy.Note, policy.ActivatedAt, now, occupantID); err != nil {
			return err
		}
	}
	return tx.DisableOverride(ctx, policy.ID, now)
}

// findOverrideForUpsert prefers an active row so reactivation can never leave
// two active rows for one client; without one it falls back to the newest row,
// including a stale row that still points at a replaced traffic row.
func (tx *Tx) findOverrideForUpsert(ctx context.Context, trafficID int64, email string) (OverridePolicy, bool, error) {
	existing, err := tx.FindActiveOverrideForClient(ctx, trafficID, email)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return OverridePolicy{}, false, err
	}
	existing, err = tx.FindOverrideForClient(ctx, trafficID, email)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return OverridePolicy{}, false, err
	}
	existing, err = tx.FindOverrideByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return OverridePolicy{}, true, nil
	}
	if err != nil {
		return OverridePolicy{}, false, err
	}
	return existing, false, nil
}

func (tx *Tx) DisableOverride(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_overrides
		SET state = ?, updated_at = ?, disabled_at = ?
		WHERE id = ?
	`, StateDisabled, now, now, id)
	return err
}

func (tx *Tx) FindActiveOverrideForClient(ctx context.Context, trafficID int64, email string) (OverridePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides
		WHERE traffic_id = ? AND email = ? AND state = ?
		ORDER BY id DESC
		LIMIT 1
	`, trafficID, email, StateActive)
	return scanOverride(row)
}

func (tx *Tx) FindOverrideByID(ctx context.Context, id int64) (OverridePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides
		WHERE id = ?
		LIMIT 1
	`, id)
	return scanOverride(row)
}

func (tx *Tx) FindOverrideForClient(ctx context.Context, trafficID int64, email string) (OverridePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides
		WHERE traffic_id = ? AND email = ?
		ORDER BY id DESC
		LIMIT 1
	`, trafficID, email)
	return scanOverride(row)
}

// FindOverrideByEmail reports the newest override row for a client regardless
// of the stored traffic row, which may point at a replaced row after a 3.x
// panel upgrade or client rename.
func (tx *Tx) FindOverrideByEmail(ctx context.Context, email string) (OverridePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides
		WHERE email = ?
		ORDER BY id DESC
		LIMIT 1
	`, email)
	return scanOverride(row)
}

func (tx *Tx) FindActiveOverrideByEmail(ctx context.Context, email string) (OverridePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides
		WHERE email = ? AND state = ?
		ORDER BY id DESC
		LIMIT 1
	`, email, StateActive)
	return scanOverride(row)
}

func (tx *Tx) ListActiveOverrides(ctx context.Context) ([]OverridePolicy, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides
		WHERE state = ?
		ORDER BY inbound_id, email, traffic_id
	`, StateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOverrideRows(rows)
}

func (s *Store) ListOverrides(ctx context.Context, includeInactive bool, inboundID *int64) ([]OverridePolicy, error) {
	exists, err := s.tableExists(ctx, "xui_factor_overrides")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	query := `
		SELECT ov.id, ov.traffic_id, ov.inbound_id, ov.email, ov.factor_ppm, ov.state, COALESCE(ov.note, ''),
			ov.created_at, ov.updated_at, ov.activated_at, ov.disabled_at
		FROM xui_factor_overrides ov`
	where := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if inboundID != nil {
		// Resolve the inbound through the authoritative clients/client_inbounds
		// link; fall back to the stored pointer only for clients whose traffic
		// row is gone.
		query += `
		LEFT JOIN client_traffics ct ON ct.id = ov.traffic_id AND ct.email = ov.email`
	}
	if !includeInactive {
		where = append(where, "ov.state = ?")
		args = append(args, StateActive)
	}
	if inboundID != nil {
		where = append(where, "("+clientInboundMembership("COALESCE(ct.email, ov.email)")+" OR (ct.id IS NULL AND ov.inbound_id = ?))")
		args = append(args, *inboundID, *inboundID)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY ov.inbound_id, ov.email, ov.traffic_id"

	rows, err := s.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOverrideRows(rows)
}

func (s *Store) CountOverrides(ctx context.Context) (OverrideCounts, error) {
	exists, err := s.tableExists(ctx, "xui_factor_overrides")
	if err != nil {
		return OverrideCounts{}, err
	}
	if !exists {
		return OverrideCounts{}, nil
	}

	rows, err := s.queryContext(ctx, `
		SELECT state, COUNT(*)
		FROM xui_factor_overrides
		GROUP BY state
	`)
	if err != nil {
		return OverrideCounts{}, err
	}
	defer rows.Close()

	var counts OverrideCounts
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return OverrideCounts{}, err
		}
		if state == StateActive {
			counts.Active += count
		} else {
			counts.Inactive += count
		}
	}
	if err := rows.Err(); err != nil {
		return OverrideCounts{}, err
	}
	return counts, nil
}

func (s *Store) CountEffectiveOverrides(ctx context.Context) (int64, error) {
	overridesReady, err := s.tableExists(ctx, "xui_factor_overrides")
	if err != nil {
		return 0, err
	}
	if !overridesReady {
		return 0, nil
	}
	excludesReady, err := s.tableExists(ctx, "xui_factor_excludes")
	if err != nil {
		return 0, err
	}

	query := `
		SELECT COUNT(DISTINCT ov.traffic_id)
		FROM xui_factor_overrides ov
		WHERE ov.state = ?
			AND EXISTS (
				SELECT 1
				FROM xui_factor_rule_clients rc
				INNER JOIN xui_factor_rules r ON r.id = rc.rule_id
				LEFT JOIN xui_factor_scopes s ON s.rule_id = r.id
				INNER JOIN client_traffics ct
					ON ct.id = rc.traffic_id
					AND ct.email = rc.email
				WHERE rc.traffic_id = ov.traffic_id
					AND rc.email = ov.email
					AND r.state = ?
					AND rc.missing_since IS NULL
					AND (
						(s.rule_id IS NULL AND ` + enableTrueExpr(s.driver, "ct.") + `)
						OR (s.rule_id IS NOT NULL AND (s.include_disabled_clients = 1 OR ` + enableTrueExpr(s.driver, "ct.") + `))
					)
			)`
	args := []any{StateActive, StateActive}
	if excludesReady {
		query += `
			AND NOT EXISTS (
				SELECT 1
				FROM xui_factor_excludes ex
				WHERE ex.traffic_id = ov.traffic_id
					AND ex.email = ov.email
					AND ex.state = ?
			)`
		args = append(args, StateActive)
	}

	var count int64
	if err := s.queryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func scanOverride(row *sql.Row) (OverridePolicy, error) {
	var policy OverridePolicy
	var activatedAt, disabledAt sql.NullInt64
	err := row.Scan(
		&policy.ID,
		&policy.TrafficID,
		&policy.InboundID,
		&policy.Email,
		&policy.FactorPPM,
		&policy.State,
		&policy.Note,
		&policy.CreatedAt,
		&policy.UpdatedAt,
		&activatedAt,
		&disabledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OverridePolicy{}, ErrNotFound
	}
	if err != nil {
		return OverridePolicy{}, err
	}
	policy.ActivatedAt = nullablePtr(activatedAt)
	policy.DisabledAt = nullablePtr(disabledAt)
	return policy, nil
}

func scanOverrideRows(rows *sql.Rows) ([]OverridePolicy, error) {
	var policies []OverridePolicy
	for rows.Next() {
		var policy OverridePolicy
		var activatedAt, disabledAt sql.NullInt64
		if err := rows.Scan(
			&policy.ID,
			&policy.TrafficID,
			&policy.InboundID,
			&policy.Email,
			&policy.FactorPPM,
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
