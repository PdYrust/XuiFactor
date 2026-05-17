package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (tx *Tx) UpsertOverride(ctx context.Context, policy OverridePolicy) (OverridePolicy, bool, error) {
	existing, err := tx.FindOverrideByIdentity(ctx, policy.TrafficID, policy.InboundID, policy.Email)
	created := false
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return OverridePolicy{}, false, err
		}
		created = true
	}

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
	updated, err := tx.FindOverrideByIdentity(ctx, policy.TrafficID, policy.InboundID, policy.Email)
	if err != nil {
		return OverridePolicy{}, false, err
	}
	if created || existing.State != StateActive {
		return updated, false, nil
	}
	return updated, existing.FactorPPM != policy.FactorPPM || strings.TrimSpace(existing.Note) != strings.TrimSpace(policy.Note), nil
}

func (tx *Tx) DisableOverride(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_overrides
		SET state = ?, updated_at = ?, disabled_at = ?
		WHERE id = ?
	`, StateDisabled, now, now, id)
	return err
}

func (tx *Tx) FindActiveOverrideForClient(ctx context.Context, trafficID, inboundID int64, email string) (OverridePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides
		WHERE traffic_id = ? AND inbound_id = ? AND email = ? AND state = ?
		LIMIT 1
	`, trafficID, inboundID, email, StateActive)
	return scanOverride(row)
}

func (tx *Tx) FindOverrideByIdentity(ctx context.Context, trafficID, inboundID int64, email string) (OverridePolicy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides
		WHERE traffic_id = ? AND inbound_id = ? AND email = ?
		LIMIT 1
	`, trafficID, inboundID, email)
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
		SELECT id, traffic_id, inbound_id, email, factor_ppm, state, COALESCE(note, ''),
			created_at, updated_at, activated_at, disabled_at
		FROM xui_factor_overrides`
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

	rows, err := s.db.QueryContext(ctx, `
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
					AND ct.inbound_id = rc.inbound_id
					AND ct.email = rc.email
				WHERE rc.traffic_id = ov.traffic_id
					AND rc.inbound_id = ov.inbound_id
					AND rc.email = ov.email
					AND r.state = ?
					AND rc.missing_since IS NULL
					AND (
						(s.rule_id IS NULL AND ct.enable = 1)
						OR (s.rule_id IS NOT NULL AND (s.include_disabled_clients = 1 OR ct.enable = 1))
					)
			)`
	args := []any{StateActive, StateActive}
	if excludesReady {
		query += `
			AND NOT EXISTS (
				SELECT 1
				FROM xui_factor_excludes ex
				WHERE ex.traffic_id = ov.traffic_id
					AND ex.inbound_id = ov.inbound_id
					AND ex.email = ov.email
					AND ex.state = ?
			)`
		args = append(args, StateActive)
	}

	var count int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
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
