package store

import (
	"context"
	"strings"
)

type RuleCounts struct {
	Active   int
	Paused   int
	Disabled int
}

func (s *Store) CountRules(ctx context.Context) (RuleCounts, error) {
	rows, err := s.queryContext(ctx, `
		SELECT state, COUNT(*)
		FROM xui_factor_rules
		GROUP BY state
	`)
	if err != nil {
		return RuleCounts{}, err
	}
	defer rows.Close()

	var counts RuleCounts
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return RuleCounts{}, err
		}
		switch state {
		case StateActive:
			counts.Active = count
		case StatePaused:
			counts.Paused = count
		case StateDisabled:
			counts.Disabled = count
		}
	}
	if err := rows.Err(); err != nil {
		return RuleCounts{}, err
	}
	return counts, nil
}

func (s *Store) CountActivePersistentScopes(ctx context.Context) (int, error) {
	var count int
	err := s.queryRowContext(ctx, `
		SELECT COUNT(*)
		FROM xui_factor_scopes s
		INNER JOIN xui_factor_rules r ON r.id = s.rule_id
		WHERE s.once = 0 AND r.state = ?
	`, StateActive).Scan(&count)
	return count, err
}

func (s *Store) CountActiveRuleClients(ctx context.Context, inboundID *int64) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM xui_factor_rule_clients rc
		INNER JOIN xui_factor_rules r ON r.id = rc.rule_id`
	where := []string{
		"r.state = ?",
		"rc.missing_since IS NULL",
	}
	args := []any{StateActive}
	if inboundID != nil {
		// Count through the authoritative clients/client_inbounds link so a
		// drifted client_traffics.inbound_id pointer does not hide clients.
		query += `
		INNER JOIN client_traffics ct ON ct.id = rc.traffic_id AND ct.email = rc.email`
		where = append(where, clientInboundMembership("ct.email"))
		args = append(args, *inboundID)
	}
	query += " WHERE " + strings.Join(where, " AND ")
	var count int
	err := s.queryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}
