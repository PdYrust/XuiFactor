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
