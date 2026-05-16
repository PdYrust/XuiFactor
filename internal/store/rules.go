package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (tx *Tx) FindClient(ctx context.Context, email string, inboundID *int64) (ClientTraffic, error) {
	query := "SELECT id, inbound_id, email, up, down, all_time FROM client_traffics WHERE email=?"
	args := []any{email}
	if inboundID != nil {
		query += " AND inbound_id=?"
		args = append(args, *inboundID)
	}
	query += " ORDER BY id LIMIT 2"

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return ClientTraffic{}, err
	}
	defer rows.Close()

	var matches []ClientTraffic
	for rows.Next() {
		var c ClientTraffic
		if err := rows.Scan(&c.ID, &c.InboundID, &c.Email, &c.Up, &c.Down, &c.AllTime); err != nil {
			return ClientTraffic{}, err
		}
		matches = append(matches, c)
	}
	if err := rows.Err(); err != nil {
		return ClientTraffic{}, err
	}
	switch len(matches) {
	case 0:
		return ClientTraffic{}, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return ClientTraffic{}, ErrAmbiguous
	}
}

func (tx *Tx) ActiveRuleForTraffic(ctx context.Context, trafficID int64) (Rule, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at
		FROM xui_factor_rules r
		INNER JOIN xui_factor_rule_clients rc ON rc.rule_id = r.id
		WHERE rc.traffic_id = ? AND r.state = ?
		ORDER BY r.id
		LIMIT 1
	`, trafficID, StateActive)
	return scanRule(row)
}

func (tx *Tx) CreateRule(ctx context.Context, rule Rule) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO xui_factor_rules(
			name, inbound_id, email, factor_ppm, state,
			created_at, updated_at, activated_at, paused_at, disabled_at
		)
		VALUES(NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
	`, rule.Name, rule.InboundID, rule.Email, rule.FactorPPM, rule.State, rule.CreatedAt, rule.UpdatedAt, rule.ActivatedAt)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (tx *Tx) CreateRuleClient(ctx context.Context, rc RuleClient) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO xui_factor_rule_clients(
			rule_id, traffic_id, inbound_id, email,
			last_up, last_down, last_all_time,
			rem_up, rem_down, rem_all_time, missing_since, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, 0, 0, 0, NULL, ?)
	`, rc.RuleID, rc.TrafficID, rc.InboundID, rc.Email, rc.LastUp, rc.LastDown, rc.LastAllTime, rc.UpdatedAt)
	return err
}

func (tx *Tx) FindRuleBySelector(ctx context.Context, email string, inboundID *int64, states ...string) (Rule, error) {
	if len(states) == 0 {
		return Rule{}, ErrNotFound
	}
	placeholders := make([]string, 0, len(states))
	args := []any{email}
	for _, state := range states {
		placeholders = append(placeholders, "?")
		args = append(args, state)
	}

	query := `
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at
		FROM xui_factor_rules r
		WHERE r.email = ? AND r.state IN (` + strings.Join(placeholders, ",") + `)`
	if inboundID != nil {
		query += " AND r.inbound_id = ?"
		args = append(args, *inboundID)
	}
	query += " ORDER BY r.id DESC LIMIT 2"

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return Rule{}, err
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		rule, err := scanRuleRows(rows)
		if err != nil {
			return Rule{}, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return Rule{}, err
	}
	switch len(rules) {
	case 0:
		return Rule{}, ErrNotFound
	case 1:
		return rules[0], nil
	default:
		return Rule{}, ErrAmbiguous
	}
}

func (tx *Tx) GetRule(ctx context.Context, id int64) (Rule, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at
		FROM xui_factor_rules r
		WHERE r.id = ?
	`, id)
	return scanRule(row)
}

func (tx *Tx) SetRuleDisabled(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rules
		SET state=?, updated_at=?, disabled_at=?
		WHERE id=?
	`, StateDisabled, now, now, id)
	return err
}

func (tx *Tx) SetRulePaused(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rules
		SET state=?, updated_at=?, paused_at=?
		WHERE id=?
	`, StatePaused, now, now, id)
	return err
}

func (tx *Tx) SetRuleActive(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rules
		SET state=?, updated_at=?, activated_at=?
		WHERE id=?
	`, StateActive, now, now, id)
	return err
}

func (tx *Tx) RefreshRuleClientBaselines(ctx context.Context, ruleID, now int64) error {
	count, err := tx.RefreshRuleClientBaselinesCount(ctx, ruleID, now)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (tx *Tx) AddEvent(ctx context.Context, ruleID *int64, eventType, message string, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO xui_factor_events(rule_id, event_type, message, created_at)
		VALUES(?, ?, ?, ?)
	`, nullableInt64(ruleID), eventType, message, now)
	return err
}

func (s *Store) ListRules(ctx context.Context, includeDisabled bool) ([]Rule, error) {
	query := `
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at,
			COUNT(rc.traffic_id) AS client_count
		FROM xui_factor_rules r
		LEFT JOIN xui_factor_rule_clients rc ON rc.rule_id = r.id`
	if includeDisabled {
		query += " GROUP BY r.id ORDER BY r.id"
	} else {
		query += " WHERE r.state IN (?, ?) GROUP BY r.id ORDER BY r.id"
	}

	var rows *sql.Rows
	var err error
	if includeDisabled {
		rows, err = s.db.QueryContext(ctx, query)
	} else {
		rows, err = s.db.QueryContext(ctx, query, StateActive, StatePaused)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		rule, err := scanRuleRowsWithCount(rows)
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

func (s *Store) ListEvents(ctx context.Context, filter EventFilter) ([]Event, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	query := `
		SELECT e.id, e.rule_id, e.event_type, COALESCE(e.message, ''), e.created_at
		FROM xui_factor_events e`
	args := make([]any, 0, 3)
	where := make([]string, 0, 2)
	if strings.TrimSpace(filter.Email) != "" || filter.InboundID != nil {
		query += `
		INNER JOIN xui_factor_rules r ON r.id = e.rule_id`
		if strings.TrimSpace(filter.Email) != "" {
			where = append(where, "r.email = ?")
			args = append(args, strings.TrimSpace(filter.Email))
		}
		if filter.InboundID != nil {
			where = append(where, "r.inbound_id = ?")
			args = append(args, *filter.InboundID)
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += `
		ORDER BY e.id DESC
		LIMIT ?`
	args = append(args, filter.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var ruleID sql.NullInt64
		if err := rows.Scan(&event.ID, &ruleID, &event.EventType, &event.Message, &event.CreatedAt); err != nil {
			return nil, err
		}
		if ruleID.Valid {
			event.RuleID = &ruleID.Int64
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func scanRule(row *sql.Row) (Rule, error) {
	var rule Rule
	var activatedAt, pausedAt, disabledAt sql.NullInt64
	err := row.Scan(
		&rule.ID,
		&rule.Name,
		&rule.InboundID,
		&rule.Email,
		&rule.FactorPPM,
		&rule.State,
		&rule.CreatedAt,
		&rule.UpdatedAt,
		&activatedAt,
		&pausedAt,
		&disabledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		return Rule{}, err
	}
	rule.ActivatedAt = nullablePtr(activatedAt)
	rule.PausedAt = nullablePtr(pausedAt)
	rule.DisabledAt = nullablePtr(disabledAt)
	return rule, nil
}

func scanRuleRows(rows *sql.Rows) (Rule, error) {
	var rule Rule
	var activatedAt, pausedAt, disabledAt sql.NullInt64
	if err := rows.Scan(
		&rule.ID,
		&rule.Name,
		&rule.InboundID,
		&rule.Email,
		&rule.FactorPPM,
		&rule.State,
		&rule.CreatedAt,
		&rule.UpdatedAt,
		&activatedAt,
		&pausedAt,
		&disabledAt,
	); err != nil {
		return Rule{}, err
	}
	rule.ActivatedAt = nullablePtr(activatedAt)
	rule.PausedAt = nullablePtr(pausedAt)
	rule.DisabledAt = nullablePtr(disabledAt)
	return rule, nil
}

func scanRuleRowsWithCount(rows *sql.Rows) (Rule, error) {
	var rule Rule
	var activatedAt, pausedAt, disabledAt sql.NullInt64
	if err := rows.Scan(
		&rule.ID,
		&rule.Name,
		&rule.InboundID,
		&rule.Email,
		&rule.FactorPPM,
		&rule.State,
		&rule.CreatedAt,
		&rule.UpdatedAt,
		&activatedAt,
		&pausedAt,
		&disabledAt,
		&rule.ClientCount,
	); err != nil {
		return Rule{}, err
	}
	rule.ActivatedAt = nullablePtr(activatedAt)
	rule.PausedAt = nullablePtr(pausedAt)
	rule.DisabledAt = nullablePtr(disabledAt)
	return rule, nil
}

func nullablePtr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func nullableInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func IsAmbiguous(err error) bool {
	return errors.Is(err, ErrAmbiguous)
}

func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict)
}

func ConflictError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, args...))
}
