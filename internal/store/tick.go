package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (tx *Tx) ListActiveRuleClients(ctx context.Context) ([]ActiveRuleClient, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at,
			rc.rule_id, rc.traffic_id, rc.inbound_id, rc.email,
			rc.last_up, rc.last_down, rc.last_all_time,
			rc.rem_up, rc.rem_down, rc.rem_all_time, rc.missing_since, rc.updated_at
		FROM xui_factor_rules r
		INNER JOIN xui_factor_rule_clients rc ON rc.rule_id = r.id
		WHERE r.state = ?
		ORDER BY r.id, rc.traffic_id
	`, StateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ActiveRuleClient
	for rows.Next() {
		var target ActiveRuleClient
		var activatedAt, pausedAt, disabledAt, missingSince sql.NullInt64
		if err := rows.Scan(
			&target.Rule.ID,
			&target.Rule.Name,
			&target.Rule.InboundID,
			&target.Rule.Email,
			&target.Rule.FactorPPM,
			&target.Rule.State,
			&target.Rule.CreatedAt,
			&target.Rule.UpdatedAt,
			&activatedAt,
			&pausedAt,
			&disabledAt,
			&target.Client.RuleID,
			&target.Client.TrafficID,
			&target.Client.InboundID,
			&target.Client.Email,
			&target.Client.LastUp,
			&target.Client.LastDown,
			&target.Client.LastAllTime,
			&target.Client.RemUp,
			&target.Client.RemDown,
			&target.Client.RemAllTime,
			&missingSince,
			&target.Client.UpdatedAt,
		); err != nil {
			return nil, err
		}
		target.Rule.ActivatedAt = nullablePtr(activatedAt)
		target.Rule.PausedAt = nullablePtr(pausedAt)
		target.Rule.DisabledAt = nullablePtr(disabledAt)
		target.Client.MissingAt = nullablePtr(missingSince)
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (tx *Tx) ClientTrafficByID(ctx context.Context, trafficID int64) (ClientTraffic, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, inbound_id, enable, email, up, down, all_time, total
		FROM client_traffics
		WHERE id = ?
		LIMIT 1
	`, trafficID)
	var c ClientTraffic
	if err := row.Scan(&c.ID, &c.InboundID, &c.Enable, &c.Email, &c.Up, &c.Down, &c.AllTime, &c.Total); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ClientTraffic{}, ErrNotFound
		}
		return ClientTraffic{}, err
	}
	return c, nil
}

func (tx *Tx) UpdateClientCountersCAS(ctx context.Context, current ClientTraffic, newUp, newDown, newAllTime int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		UPDATE client_traffics
		SET up = ?, down = ?, all_time = ?
		WHERE id = ?
			AND inbound_id = ?
			AND email = ?
			AND up = ?
			AND down = ?
			AND all_time = ?
	`, newUp, newDown, newAllTime, current.ID, current.InboundID, current.Email, current.Up, current.Down, current.AllTime)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	switch rows {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("unsafe counter update matched %d rows", rows)
	}
}

func (tx *Tx) UpdateRuleClientBaseline(ctx context.Context, rc RuleClient) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rule_clients
		SET last_up = ?,
			last_down = ?,
			last_all_time = ?,
			rem_up = ?,
			rem_down = ?,
			rem_all_time = ?,
			missing_since = NULL,
			updated_at = ?
		WHERE rule_id = ? AND traffic_id = ?
	`, rc.LastUp, rc.LastDown, rc.LastAllTime, rc.RemUp, rc.RemDown, rc.RemAllTime, rc.UpdatedAt, rc.RuleID, rc.TrafficID)
	return err
}

func (tx *Tx) MarkRuleClientMissing(ctx context.Context, ruleID, trafficID, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rule_clients
		SET missing_since = COALESCE(missing_since, ?),
			updated_at = ?
		WHERE rule_id = ? AND traffic_id = ?
	`, now, now, ruleID, trafficID)
	return err
}
