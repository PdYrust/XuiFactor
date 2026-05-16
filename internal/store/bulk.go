package store

import (
	"context"
	"strings"
)

func (tx *Tx) ListClientCandidates(ctx context.Context, filter ClientFilter) ([]ClientTraffic, error) {
	query := `
		SELECT id, inbound_id, enable, email, up, down, all_time, total
		FROM client_traffics
		WHERE email <> ''`
	args := make([]any, 0, 3)
	if !filter.IncludeDisabledClients {
		query += " AND enable = 1"
	}
	if filter.LimitedOnly {
		query += " AND total > 0"
	}
	if filter.InboundID != nil {
		query += " AND inbound_id = ?"
		args = append(args, *filter.InboundID)
	}
	query += " ORDER BY inbound_id, email, id"

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []ClientTraffic
	for rows.Next() {
		var c ClientTraffic
		if err := rows.Scan(&c.ID, &c.InboundID, &c.Enable, &c.Email, &c.Up, &c.Down, &c.AllTime, &c.Total); err != nil {
			return nil, err
		}
		clients = append(clients, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return clients, nil
}

func (tx *Tx) RulesForTrafficInStates(ctx context.Context, trafficID int64, states ...string) ([]Rule, error) {
	if len(states) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(states))
	args := []any{trafficID}
	for _, state := range states {
		placeholders = append(placeholders, "?")
		args = append(args, state)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at
		FROM xui_factor_rules r
		INNER JOIN xui_factor_rule_clients rc ON rc.rule_id = r.id
		WHERE rc.traffic_id = ? AND r.state IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY r.id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		rule, err := scanRuleRows(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func (tx *Tx) RefreshRuleClientBaselinesCount(ctx context.Context, ruleID, now int64) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT rc.traffic_id, ct.inbound_id, ct.email, ct.up, ct.down, ct.all_time
		FROM xui_factor_rule_clients rc
		INNER JOIN client_traffics ct
			ON ct.id = rc.traffic_id
			AND ct.inbound_id = rc.inbound_id
			AND ct.email = rc.email
		WHERE rc.rule_id = ?
	`, ruleID)
	if err != nil {
		return 0, err
	}

	var clients []ClientTraffic
	for rows.Next() {
		var c ClientTraffic
		if err := rows.Scan(&c.ID, &c.InboundID, &c.Email, &c.Up, &c.Down, &c.AllTime); err != nil {
			rows.Close()
			return 0, err
		}
		clients = append(clients, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	for _, c := range clients {
		if _, err := tx.ExecContext(ctx, `
			UPDATE xui_factor_rule_clients
			SET inbound_id=?, email=?, last_up=?, last_down=?, last_all_time=?,
				rem_up=0, rem_down=0, rem_all_time=0, missing_since=NULL, updated_at=?
			WHERE rule_id=? AND traffic_id=?
		`, c.InboundID, c.Email, c.Up, c.Down, c.AllTime, now, ruleID, c.ID); err != nil {
			return 0, err
		}
	}
	return len(clients), nil
}
