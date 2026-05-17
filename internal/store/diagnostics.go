package store

import "context"

type RuleCounts struct {
	Active   int
	Paused   int
	Disabled int
}

func (s *Store) CountRules(ctx context.Context) (RuleCounts, error) {
	rows, err := s.db.QueryContext(ctx, `
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
	err := s.db.QueryRowContext(ctx, `
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
		INNER JOIN xui_factor_rules r ON r.id = rc.rule_id
		WHERE r.state = ?
			AND rc.missing_since IS NULL`
	args := []any{StateActive}
	if inboundID != nil {
		query += `
			AND rc.inbound_id = ?`
		args = append(args, *inboundID)
	}
	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}
