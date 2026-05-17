package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (tx *Tx) FindClient(ctx context.Context, email string, inboundID *int64) (ClientTraffic, error) {
	query := "SELECT id, inbound_id, enable, email, up, down, all_time, total FROM client_traffics WHERE email=?"
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
		if err := rows.Scan(&c.ID, &c.InboundID, &c.Enable, &c.Email, &c.Up, &c.Down, &c.AllTime, &c.Total); err != nil {
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

func (tx *Tx) SetRuleMerged(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rules
		SET state=?, updated_at=?, disabled_at=?
		WHERE id=?
	`, StateMerged, now, now, id)
	return err
}

func (tx *Tx) SetRuleOrphaned(ctx context.Context, id, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rules
		SET state=?, updated_at=?, disabled_at=?
		WHERE id=?
	`, StateOrphaned, now, now, id)
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
	excludesReady, err := s.tableExists(ctx, "xui_factor_excludes")
	if err != nil {
		return nil, err
	}
	overridesReady, err := s.tableExists(ctx, "xui_factor_overrides")
	if err != nil {
		return nil, err
	}
	baseValidity := `
				rc.missing_since IS NULL
					AND ct.id IS NOT NULL
					AND (
						(s.rule_id IS NULL AND ct.enable = 1)
						OR (s.rule_id IS NOT NULL AND (s.include_disabled_clients = 1 OR ct.enable = 1))
					)
					AND (
						s.rule_id IS NOT NULL
						OR NOT EXISTS (
							SELECT 1
							FROM xui_factor_rule_clients scope_rc
							INNER JOIN xui_factor_rules scope_r ON scope_r.id = scope_rc.rule_id
							INNER JOIN xui_factor_scopes scope_s ON scope_s.rule_id = scope_r.id
							WHERE scope_rc.traffic_id = rc.traffic_id
								AND scope_r.state = ?
								AND scope_r.id <> r.id
						)
					)`
	effectiveValidity := baseValidity
	args := []any{StateActive, StateActive}
	if excludesReady {
		effectiveValidity += `
					AND NOT EXISTS (
						SELECT 1
						FROM xui_factor_excludes ex
						WHERE ex.traffic_id = rc.traffic_id
							AND ex.inbound_id = rc.inbound_id
							AND ex.email = rc.email
							AND ex.state = ?
					)`
		args = append(args, StateActive)
	}
	if overridesReady {
		effectiveValidity += `
					AND NOT EXISTS (
						SELECT 1
						FROM xui_factor_overrides ov
						WHERE ov.traffic_id = rc.traffic_id
							AND ov.inbound_id = rc.inbound_id
							AND ov.email = rc.email
							AND ov.state = ?
					)`
		args = append(args, StateActive)
	}

	query := `
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at,
			COALESCE(SUM(CASE WHEN ` + baseValidity + ` THEN 1 ELSE 0 END), 0) AS client_count,
			COALESCE(SUM(CASE WHEN ` + effectiveValidity + ` THEN 1 ELSE 0 END), 0) AS effective_client_count,
			s.rule_id, s.inbound_id, s.limited_only, s.include_disabled_clients, s.once, s.created_at, s.updated_at
		FROM xui_factor_rules r
		LEFT JOIN xui_factor_rule_clients rc ON rc.rule_id = r.id
		LEFT JOIN client_traffics ct
			ON ct.id = rc.traffic_id
			AND ct.inbound_id = rc.inbound_id
			AND ct.email = rc.email
		LEFT JOIN xui_factor_scopes s ON s.rule_id = r.id`
	if includeDisabled {
		query += " GROUP BY r.id ORDER BY r.id"
	} else {
		query += " WHERE r.state IN (?, ?) GROUP BY r.id ORDER BY r.id"
		args = append(args, StateActive, StatePaused)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		rule, err := scanRuleRowsWithCountAndScope(rows)
		if err != nil {
			return nil, err
		}
		if !includeDisabled && rule.Scope == nil && rule.EffectiveClientCount == 0 {
			continue
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func (s *Store) ListEvents(ctx context.Context, filter EventFilter) ([]Event, error) {
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}
	postFilter := strings.TrimSpace(filter.Email) != "" || filter.InboundID != nil || filter.PolicyID != nil
	query := `
		SELECT e.id, e.rule_id, e.event_type, COALESCE(e.message, ''), e.created_at,
			COALESCE(r.email, ''), COALESCE(s.inbound_id, r.inbound_id)
		FROM xui_factor_events e
		LEFT JOIN xui_factor_rules r ON r.id = e.rule_id
		LEFT JOIN xui_factor_scopes s ON s.rule_id = r.id`
	args := make([]any, 0, 4)
	where := make([]string, 0, 3)
	if strings.TrimSpace(filter.EventType) != "" {
		where = append(where, "e.event_type = ?")
		args = append(args, strings.TrimSpace(filter.EventType))
	}
	if filter.RuleID != nil {
		where = append(where, "e.rule_id = ?")
		args = append(args, *filter.RuleID)
	}
	if filter.Since != nil {
		where = append(where, "e.created_at >= ?")
		args = append(args, *filter.Since)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += `
		ORDER BY e.id DESC`
	if !postFilter && limit > 0 {
		query += `
		LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var ruleID sql.NullInt64
		var inboundID sql.NullInt64
		var ruleEmail string
		if err := rows.Scan(&event.ID, &ruleID, &event.EventType, &event.Message, &event.CreatedAt, &ruleEmail, &inboundID); err != nil {
			return nil, err
		}
		if ruleID.Valid {
			event.RuleID = &ruleID.Int64
		}
		event.Email = ruleEmail
		if inboundID.Valid {
			event.InboundID = &inboundID.Int64
		}
		hydrateEventFromMessage(&event)
		if !eventMatchesFilter(event, filter) {
			continue
		}
		events = append(events, event)
		if postFilter && limit > 0 && len(events) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) TrafficImpact(ctx context.Context, filter EventFilter) (TrafficImpact, error) {
	filter.EventType = EventTrafficApply
	filter.Limit = -1
	events, err := s.ListEvents(ctx, filter)
	if err != nil {
		return TrafficImpact{}, err
	}
	var impact TrafficImpact
	for _, event := range events {
		impact.Applications++
		if impact.LastAppliedAt == nil || event.CreatedAt > *impact.LastAppliedAt {
			createdAt := event.CreatedAt
			impact.LastAppliedAt = &createdAt
		}
		extra, ok := eventExtraAllTime(event.Message)
		if !ok || extra <= 0 {
			continue
		}
		const maxInt64 = int64(^uint64(0) >> 1)
		if impact.ExtraBytes > maxInt64-extra {
			impact.ExtraBytes = maxInt64
			continue
		}
		impact.ExtraBytes += extra
	}
	return impact, nil
}

func eventMatchesFilter(event Event, filter EventFilter) bool {
	if strings.TrimSpace(filter.Email) != "" && event.Email != strings.TrimSpace(filter.Email) {
		return false
	}
	if filter.InboundID != nil {
		if event.InboundID == nil || *event.InboundID != *filter.InboundID {
			return false
		}
	}
	if filter.PolicyID != nil {
		if event.PolicyID == nil || *event.PolicyID != *filter.PolicyID {
			return false
		}
	}
	return true
}

func hydrateEventFromMessage(event *Event) {
	fields := strings.Fields(event.Message)
	for i, field := range fields {
		clean := strings.Trim(field, " ,.;:")
		switch {
		case clean == "policy" && i+1 < len(fields):
			if id, err := strconv.ParseInt(strings.Trim(fields[i+1], " ,.;:"), 10, 64); err == nil {
				event.PolicyID = &id
			}
		case clean == "inbound" && i+1 < len(fields):
			if id, err := strconv.ParseInt(strings.Trim(fields[i+1], " ,.;:"), 10, 64); err == nil {
				event.InboundID = &id
			}
		case strings.HasPrefix(clean, "inbound="):
			if id, err := strconv.ParseInt(strings.TrimPrefix(clean, "inbound="), 10, 64); err == nil {
				event.InboundID = &id
			}
		case clean == "email" && i+1 < len(fields):
			event.Email = strings.Trim(fields[i+1], " ,.;:")
		case strings.HasPrefix(clean, "email="):
			event.Email = strings.TrimPrefix(clean, "email=")
		}
	}
}

func eventExtraAllTime(message string) (int64, bool) {
	for _, field := range strings.Fields(message) {
		clean := strings.Trim(field, " ,.;:")
		if !strings.HasPrefix(clean, "extra_all_time=") {
			continue
		}
		value := strings.TrimPrefix(clean, "extra_all_time=")
		extra, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, false
		}
		return extra, true
	}
	return 0, false
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
