package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// UpsertOverride reports whether an already active override changed.
func (tx *Tx) UpsertOverride(ctx context.Context, policy OverridePolicy) (OverridePolicy, bool, error) {
	existing, found, err := tx.findOverrideForUpsert(ctx, policy.TrafficID, policy.Email)
	if err != nil {
		return OverridePolicy{}, false, err
	}
	note := strings.TrimSpace(policy.Note)
	if !found {
		id, err := tx.insertReturningID(ctx, `
			INSERT INTO xui_factor_overrides(
				traffic_id, inbound_id, email, factor_ppm, state, note,
				created_at, updated_at, activated_at, disabled_at
			)
			VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, NULL)
		`, policy.TrafficID, policy.InboundID, policy.Email, policy.FactorPPM, StateActive, note, policy.CreatedAt, policy.UpdatedAt, policy.ActivatedAt)
		if err != nil {
			return OverridePolicy{}, false, err
		}
		created, err := tx.FindOverrideByID(ctx, id)
		return created, false, err
	}

	changed := existing.State == StateActive &&
		(existing.FactorPPM != policy.FactorPPM || strings.TrimSpace(existing.Note) != note)
	if _, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_overrides
		SET traffic_id = ?, inbound_id = ?, factor_ppm = ?, state = ?, note = NULLIF(?, ''),
			updated_at = ?, activated_at = ?, disabled_at = NULL
		WHERE id = ?
	`, policy.TrafficID, policy.InboundID, policy.FactorPPM, StateActive, note, policy.UpdatedAt, policy.ActivatedAt, existing.ID); err != nil {
		return OverridePolicy{}, false, err
	}
	updated, err := tx.FindOverrideByID(ctx, existing.ID)
	return updated, changed, err
}

// RelinkOverrideRow moves an active override onto the given traffic row. If
// another row already holds the target (traffic, inbound, email) tuple, an
// active holder keeps the policy and this row is disabled; an inactive holder
// is reactivated with this row's factor instead.
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

// findOverrideForUpsert follows the same preference order as
// findExcludeForUpsert.
func (tx *Tx) findOverrideForUpsert(ctx context.Context, trafficID int64, email string) (OverridePolicy, bool, error) {
	existing, err := tx.FindActiveOverrideForClient(ctx, trafficID, email)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return OverridePolicy{}, false, err
	}
	existing, err = tx.FindOverrideForClient(ctx, trafficID, email)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return OverridePolicy{}, false, err
	}
	existing, err = tx.FindOverrideByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return OverridePolicy{}, false, nil
	}
	if err != nil {
		return OverridePolicy{}, false, err
	}
	return existing, true, nil
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
		// Policies whose traffic row is gone fall back to the stored inbound.
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
