package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (tx *Tx) CreateScope(ctx context.Context, scope Scope) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO xui_factor_scopes(
			rule_id, inbound_id, limited_only, include_disabled_clients, once, created_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, scope.RuleID, nullableInt64(scope.InboundID), boolInt(scope.LimitedOnly), boolInt(scope.IncludeDisabledClients), boolInt(scope.Once), scope.CreatedAt, scope.UpdatedAt)
	return err
}

func (tx *Tx) FindActivePersistentScope(ctx context.Context, factorPPM int64, inboundID *int64, limitedOnly, includeDisabledClients bool) (Scope, error) {
	query := `
		SELECT
			s.rule_id, s.inbound_id, s.limited_only, s.include_disabled_clients, s.once, s.created_at, s.updated_at,
			r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at
		FROM xui_factor_scopes s
		INNER JOIN xui_factor_rules r ON r.id = s.rule_id
		WHERE r.state = ?
			AND r.factor_ppm = ?
			AND s.once = 0
			AND s.limited_only = ?
			AND s.include_disabled_clients = ?`
	args := []any{StateActive, factorPPM, boolInt(limitedOnly), boolInt(includeDisabledClients)}
	if inboundID == nil {
		query += " AND s.inbound_id IS NULL"
	} else {
		query += " AND s.inbound_id = ?"
		args = append(args, *inboundID)
	}
	query += " ORDER BY s.rule_id LIMIT 1"

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return Scope{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Scope{}, err
		}
		return Scope{}, ErrNotFound
	}
	scope, err := scanScopeRuleRows(rows)
	if err != nil {
		return Scope{}, err
	}
	if err := rows.Err(); err != nil {
		return Scope{}, err
	}
	return scope, nil
}

func (tx *Tx) RuleHasScope(ctx context.Context, ruleID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM xui_factor_scopes
		WHERE rule_id = ?
		LIMIT 1
	`, ruleID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (tx *Tx) RuleClientExists(ctx context.Context, ruleID, trafficID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM xui_factor_rule_clients
		WHERE rule_id = ? AND traffic_id = ?
		LIMIT 1
	`, ruleID, trafficID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (tx *Tx) RuleClient(ctx context.Context, ruleID, trafficID int64) (RuleClient, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT rule_id, traffic_id, inbound_id, email,
			last_up, last_down, last_all_time,
			rem_up, rem_down, rem_all_time, missing_since, updated_at
		FROM xui_factor_rule_clients
		WHERE rule_id = ? AND traffic_id = ?
	`, ruleID, trafficID)
	return scanRuleClient(row)
}

func (tx *Tx) RuleClientsForRule(ctx context.Context, ruleID int64) ([]RuleClient, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT rule_id, traffic_id, inbound_id, email,
			last_up, last_down, last_all_time,
			rem_up, rem_down, rem_all_time, missing_since, updated_at
		FROM xui_factor_rule_clients
		WHERE rule_id = ?
		ORDER BY traffic_id
	`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []RuleClient
	for rows.Next() {
		rc, err := scanRuleClientRows(rows)
		if err != nil {
			return nil, err
		}
		clients = append(clients, rc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return clients, nil
}

func (tx *Tx) RuleClientCount(ctx context.Context, ruleID int64) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM xui_factor_rule_clients
		WHERE rule_id = ?
	`, ruleID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (tx *Tx) CopyRuleClient(ctx context.Context, fromRuleID, toRuleID, trafficID int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO xui_factor_rule_clients(
			rule_id, traffic_id, inbound_id, email,
			last_up, last_down, last_all_time,
			rem_up, rem_down, rem_all_time, missing_since, updated_at
		)
		SELECT ?, traffic_id, inbound_id, email,
			last_up, last_down, last_all_time,
			rem_up, rem_down, rem_all_time, missing_since, updated_at
		FROM xui_factor_rule_clients
		WHERE rule_id = ? AND traffic_id = ?
	`, toRuleID, fromRuleID, trafficID)
	return err
}

func (tx *Tx) ActiveRuleForTrafficExcept(ctx context.Context, trafficID, exceptRuleID int64) (Rule, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at
		FROM xui_factor_rules r
		INNER JOIN xui_factor_rule_clients rc ON rc.rule_id = r.id
		WHERE rc.traffic_id = ? AND r.state = ? AND r.id <> ?
		ORDER BY r.id
		LIMIT 1
	`, trafficID, StateActive, exceptRuleID)
	return scanRule(row)
}

func (tx *Tx) ActiveScopeForTrafficExcept(ctx context.Context, trafficID, exceptRuleID int64) (Rule, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at
		FROM xui_factor_rules r
		INNER JOIN xui_factor_rule_clients rc ON rc.rule_id = r.id
		INNER JOIN xui_factor_scopes s ON s.rule_id = r.id
		WHERE rc.traffic_id = ? AND r.state = ? AND r.id <> ?
		ORDER BY r.id
		LIMIT 1
	`, trafficID, StateActive, exceptRuleID)
	return scanRule(row)
}

func (tx *Tx) ActiveConflictForRule(ctx context.Context, ruleID int64) (Rule, int64, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at,
			rc.traffic_id
		FROM xui_factor_rule_clients self
		INNER JOIN xui_factor_rule_clients rc ON rc.traffic_id = self.traffic_id
		INNER JOIN xui_factor_rules r ON r.id = rc.rule_id
		WHERE self.rule_id = ? AND r.state = ? AND r.id <> ?
		ORDER BY rc.traffic_id, r.id
		LIMIT 1
	`, ruleID, StateActive, ruleID)

	var rule Rule
	var trafficID int64
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
		&trafficID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, 0, ErrNotFound
	}
	if err != nil {
		return Rule{}, 0, err
	}
	rule.ActivatedAt = nullablePtr(activatedAt)
	rule.PausedAt = nullablePtr(pausedAt)
	rule.DisabledAt = nullablePtr(disabledAt)
	return rule, trafficID, nil
}

func (tx *Tx) ListActivePersistentScopes(ctx context.Context) ([]Scope, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			s.rule_id, s.inbound_id, s.limited_only, s.include_disabled_clients, s.once, s.created_at, s.updated_at,
			r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at
		FROM xui_factor_scopes s
		INNER JOIN xui_factor_rules r ON r.id = s.rule_id
		WHERE r.state = ? AND s.once = 0
		ORDER BY s.rule_id
	`, StateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scopes []Scope
	for rows.Next() {
		scope, err := scanScopeRuleRows(rows)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return scopes, nil
}

func (tx *Tx) ListRulesInStates(ctx context.Context, inboundID *int64, states ...string) ([]Rule, error) {
	if len(states) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(states))
	args := make([]any, 0, len(states)+1)
	for _, state := range states {
		placeholders = append(placeholders, "?")
		args = append(args, state)
	}
	query := `
		SELECT
			r.id, COALESCE(r.name, ''), COALESCE(r.inbound_id, 0), r.email, r.factor_ppm, r.state,
			r.created_at, r.updated_at, r.activated_at, r.paused_at, r.disabled_at,
			s.rule_id, s.inbound_id, s.limited_only, s.include_disabled_clients, s.once, s.created_at, s.updated_at
		FROM xui_factor_rules r
		LEFT JOIN xui_factor_scopes s ON s.rule_id = r.id
		WHERE r.state IN (` + strings.Join(placeholders, ",") + `)`
	if inboundID != nil {
		query += " AND r.inbound_id = ?"
		args = append(args, *inboundID)
	}
	query += " ORDER BY r.id"

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		rule, err := scanRuleRowsWithScope(rows)
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

func scanRuleRowsWithScope(rows *sql.Rows) (Rule, error) {
	var rule Rule
	var activatedAt, pausedAt, disabledAt sql.NullInt64
	var scopeRuleID, scopeInboundID, scopeCreatedAt, scopeUpdatedAt sql.NullInt64
	var limitedOnly, includeDisabledClients, once sql.NullInt64
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
		&scopeRuleID,
		&scopeInboundID,
		&limitedOnly,
		&includeDisabledClients,
		&once,
		&scopeCreatedAt,
		&scopeUpdatedAt,
	); err != nil {
		return Rule{}, err
	}
	rule.ActivatedAt = nullablePtr(activatedAt)
	rule.PausedAt = nullablePtr(pausedAt)
	rule.DisabledAt = nullablePtr(disabledAt)
	attachScope(&rule, scopeRuleID, scopeInboundID, limitedOnly, includeDisabledClients, once, scopeCreatedAt, scopeUpdatedAt)
	return rule, nil
}

func scanRuleClient(row *sql.Row) (RuleClient, error) {
	var rc RuleClient
	var missingSince sql.NullInt64
	err := row.Scan(
		&rc.RuleID,
		&rc.TrafficID,
		&rc.InboundID,
		&rc.Email,
		&rc.LastUp,
		&rc.LastDown,
		&rc.LastAllTime,
		&rc.RemUp,
		&rc.RemDown,
		&rc.RemAllTime,
		&missingSince,
		&rc.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RuleClient{}, ErrNotFound
	}
	if err != nil {
		return RuleClient{}, err
	}
	rc.MissingAt = nullablePtr(missingSince)
	return rc, nil
}

func scanRuleClientRows(rows *sql.Rows) (RuleClient, error) {
	var rc RuleClient
	var missingSince sql.NullInt64
	if err := rows.Scan(
		&rc.RuleID,
		&rc.TrafficID,
		&rc.InboundID,
		&rc.Email,
		&rc.LastUp,
		&rc.LastDown,
		&rc.LastAllTime,
		&rc.RemUp,
		&rc.RemDown,
		&rc.RemAllTime,
		&missingSince,
		&rc.UpdatedAt,
	); err != nil {
		return RuleClient{}, err
	}
	rc.MissingAt = nullablePtr(missingSince)
	return rc, nil
}

func scanRuleRowsWithCountAndScope(rows *sql.Rows) (Rule, error) {
	var rule Rule
	var activatedAt, pausedAt, disabledAt sql.NullInt64
	var scopeRuleID, scopeInboundID, scopeCreatedAt, scopeUpdatedAt sql.NullInt64
	var limitedOnly, includeDisabledClients, once sql.NullInt64
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
		&rule.EffectiveClientCount,
		&scopeRuleID,
		&scopeInboundID,
		&limitedOnly,
		&includeDisabledClients,
		&once,
		&scopeCreatedAt,
		&scopeUpdatedAt,
	); err != nil {
		return Rule{}, err
	}
	rule.ActivatedAt = nullablePtr(activatedAt)
	rule.PausedAt = nullablePtr(pausedAt)
	rule.DisabledAt = nullablePtr(disabledAt)
	attachScope(&rule, scopeRuleID, scopeInboundID, limitedOnly, includeDisabledClients, once, scopeCreatedAt, scopeUpdatedAt)
	return rule, nil
}

func scanScopeRuleRows(rows *sql.Rows) (Scope, error) {
	var scope Scope
	var scopeInboundID sql.NullInt64
	var limitedOnly, includeDisabledClients, once int64
	var activatedAt, pausedAt, disabledAt sql.NullInt64
	if err := rows.Scan(
		&scope.RuleID,
		&scopeInboundID,
		&limitedOnly,
		&includeDisabledClients,
		&once,
		&scope.CreatedAt,
		&scope.UpdatedAt,
		&scope.Rule.ID,
		&scope.Rule.Name,
		&scope.Rule.InboundID,
		&scope.Rule.Email,
		&scope.Rule.FactorPPM,
		&scope.Rule.State,
		&scope.Rule.CreatedAt,
		&scope.Rule.UpdatedAt,
		&activatedAt,
		&pausedAt,
		&disabledAt,
	); err != nil {
		return Scope{}, err
	}
	scope.InboundID = nullablePtr(scopeInboundID)
	scope.LimitedOnly = limitedOnly != 0
	scope.IncludeDisabledClients = includeDisabledClients != 0
	scope.Once = once != 0
	scope.Rule.ActivatedAt = nullablePtr(activatedAt)
	scope.Rule.PausedAt = nullablePtr(pausedAt)
	scope.Rule.DisabledAt = nullablePtr(disabledAt)
	copyScope := scope
	copyScope.Rule = Rule{}
	scope.Rule.Scope = &copyScope
	return scope, nil
}

func attachScope(rule *Rule, scopeRuleID, scopeInboundID, limitedOnly, includeDisabledClients, once, createdAt, updatedAt sql.NullInt64) {
	if !scopeRuleID.Valid {
		return
	}
	rule.Scope = &Scope{
		RuleID:                 scopeRuleID.Int64,
		InboundID:              nullablePtr(scopeInboundID),
		LimitedOnly:            limitedOnly.Valid && limitedOnly.Int64 != 0,
		IncludeDisabledClients: includeDisabledClients.Valid && includeDisabledClients.Int64 != 0,
		Once:                   once.Valid && once.Int64 != 0,
		CreatedAt:              createdAt.Int64,
		UpdatedAt:              updatedAt.Int64,
	}
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func ScopeDescription(scope *Scope) string {
	if scope == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	if scope.Once {
		parts = append(parts, "snapshot")
	} else {
		parts = append(parts, "persistent")
	}
	if scope.InboundID == nil {
		parts = append(parts, "all-enabled")
	} else {
		parts = append(parts, fmt.Sprintf("inbound=%d", *scope.InboundID))
	}
	if scope.LimitedOnly {
		parts = append(parts, "limited-only")
	}
	if scope.IncludeDisabledClients {
		parts = append(parts, "include-disabled")
	}
	return strings.Join(parts, ",")
}
