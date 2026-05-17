package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type cleanupRuleClient struct {
	RuleID    int64
	TrafficID int64
	InboundID int64
	Email     string
	MissingAt *int64
}

func (s *Store) Cleanup(ctx context.Context, opts CleanupOptions) (CleanupResult, error) {
	var result CleanupResult
	result.DryRun = opts.DryRun
	err := s.WithImmediateTx(ctx, func(tx *Tx) error {
		var err error
		result, err = tx.Cleanup(ctx, opts)
		return err
	})
	return result, err
}

func (s *Store) AutoCleanup(ctx context.Context, opts CleanupOptions) (CleanupResult, bool, error) {
	var result CleanupResult
	ran := false
	err := s.WithImmediateTx(ctx, func(tx *Tx) error {
		lastRun, ok, err := tx.GetMetaInt64(ctx, "last_auto_cleanup_at")
		if err != nil {
			return err
		}
		interval := opts.MissingClientGrace
		if interval <= 0 {
			return errors.New("missing client grace must be positive")
		}
		if ok && opts.Now-lastRun < interval {
			return nil
		}
		result, err = tx.Cleanup(ctx, opts)
		if err != nil {
			return err
		}
		if err := tx.SetMetaInt64(ctx, "last_auto_cleanup_at", opts.Now); err != nil {
			return err
		}
		ran = true
		return nil
	})
	return result, ran, err
}

func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "VACUUM")
	return wrapSQLiteError("vacuum sqlite database", err)
}

func (tx *Tx) GetMetaInt64(ctx context.Context, key string) (int64, bool, error) {
	var value int64
	err := tx.QueryRowContext(ctx, `
		SELECT CAST(value AS INTEGER)
		FROM xui_factor_meta
		WHERE key = ?
	`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return value, true, nil
}

func (tx *Tx) SetMetaInt64(ctx context.Context, key string, value int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO xui_factor_meta(key, value, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, fmt.Sprintf("%d", value), value)
	return err
}

func (tx *Tx) Cleanup(ctx context.Context, opts CleanupOptions) (CleanupResult, error) {
	if opts.Now <= 0 {
		return CleanupResult{}, errors.New("cleanup now must be positive")
	}
	if opts.MissingClientGrace <= 0 {
		return CleanupResult{}, errors.New("missing client grace must be positive")
	}
	if opts.DisabledRuleRetention <= 0 {
		return CleanupResult{}, errors.New("disabled rule retention must be positive")
	}
	if opts.AuditRetention <= 0 {
		return CleanupResult{}, errors.New("audit retention must be positive")
	}

	result := CleanupResult{DryRun: opts.DryRun}
	marked, err := tx.markMismatchedRuleClients(ctx, opts.Now, opts.DryRun)
	if err != nil {
		return CleanupResult{}, err
	}
	result.MissingClientsMarked = marked

	missingCutoff := opts.Now - opts.MissingClientGrace
	prunedMissing, err := tx.pruneMissingRuleClients(ctx, missingCutoff, opts.Now, opts.DryRun)
	if err != nil {
		return CleanupResult{}, err
	}
	result.MissingClientsPruned = prunedMissing

	disabledCutoff := opts.Now - opts.DisabledRuleRetention
	prunedRules, prunedScopes, err := tx.pruneDisabledRules(ctx, disabledCutoff, opts.Now, opts.DryRun)
	if err != nil {
		return CleanupResult{}, err
	}
	result.DisabledRulesPruned = prunedRules
	result.DisabledScopesPruned = prunedScopes

	inactiveExcludes, err := tx.pruneInactiveExcludes(ctx, disabledCutoff, opts.Now, opts.DryRun)
	if err != nil {
		return CleanupResult{}, err
	}
	result.InactiveExcludesPruned = inactiveExcludes

	inactiveOverrides, err := tx.pruneInactiveOverrides(ctx, disabledCutoff, opts.Now, opts.DryRun)
	if err != nil {
		return CleanupResult{}, err
	}
	result.InactiveOverridesPruned = inactiveOverrides

	auditCutoff := opts.Now - opts.AuditRetention
	auditEvents, err := tx.pruneAuditEvents(ctx, auditCutoff, opts.DryRun)
	if err != nil {
		return CleanupResult{}, err
	}
	result.AuditEventsPruned = auditEvents
	return result, nil
}

func (tx *Tx) markMismatchedRuleClients(ctx context.Context, now int64, dryRun bool) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT rule_id, traffic_id, inbound_id, email, missing_since
		FROM xui_factor_rule_clients
		WHERE missing_since IS NULL
		ORDER BY rule_id, traffic_id
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var clients []cleanupRuleClient
	for rows.Next() {
		var client cleanupRuleClient
		var missing sql.NullInt64
		if err := rows.Scan(&client.RuleID, &client.TrafficID, &client.InboundID, &client.Email, &missing); err != nil {
			return 0, err
		}
		client.MissingAt = nullablePtr(missing)
		clients = append(clients, client)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	marked := 0
	for _, client := range clients {
		current, err := tx.ClientTrafficByID(ctx, client.TrafficID)
		missing := errors.Is(err, ErrNotFound)
		if err != nil && !missing {
			return 0, err
		}
		mismatched := !missing && (current.InboundID != client.InboundID || current.Email != client.Email)
		if !missing && !mismatched {
			continue
		}
		marked++
		if dryRun {
			continue
		}
		if err := tx.MarkRuleClientMissing(ctx, client.RuleID, client.TrafficID, now); err != nil {
			return 0, err
		}
		message := fmt.Sprintf("traffic id %d is missing or no longer matches rule target", client.TrafficID)
		if err := tx.AddEvent(ctx, &client.RuleID, EventClientMissing, message, now); err != nil {
			return 0, err
		}
	}
	return marked, nil
}

func (tx *Tx) pruneMissingRuleClients(ctx context.Context, cutoff, now int64, dryRun bool) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT rule_id, traffic_id
		FROM xui_factor_rule_clients
		WHERE missing_since IS NOT NULL AND missing_since <= ?
		ORDER BY rule_id, traffic_id
	`, cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type target struct {
		ruleID    int64
		trafficID int64
	}
	var targets []target
	for rows.Next() {
		var target target
		if err := rows.Scan(&target.ruleID, &target.trafficID); err != nil {
			return 0, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if dryRun || len(targets) == 0 {
		return len(targets), nil
	}

	for _, target := range targets {
		_, err := tx.ExecContext(ctx, `
			DELETE FROM xui_factor_rule_clients
			WHERE rule_id = ? AND traffic_id = ?
		`, target.ruleID, target.trafficID)
		if err != nil {
			return 0, err
		}
		message := fmt.Sprintf("traffic id %d missing client tracking pruned", target.trafficID)
		if err := tx.AddEvent(ctx, &target.ruleID, EventClientPruned, message, now); err != nil {
			return 0, err
		}
	}
	return len(targets), nil
}

func (tx *Tx) pruneDisabledRules(ctx context.Context, cutoff, now int64, dryRun bool) (int, int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT r.id, CASE WHEN s.rule_id IS NULL THEN 0 ELSE 1 END AS is_scope
		FROM xui_factor_rules r
		LEFT JOIN xui_factor_scopes s ON s.rule_id = r.id
		WHERE r.state IN (?, ?, ?)
			AND COALESCE(r.disabled_at, r.updated_at) <= ?
		ORDER BY r.id
	`, StateDisabled, StateMerged, StateOrphaned, cutoff)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	type target struct {
		ruleID  int64
		isScope bool
	}
	var targets []target
	for rows.Next() {
		var target target
		var isScope int64
		if err := rows.Scan(&target.ruleID, &isScope); err != nil {
			return 0, 0, err
		}
		target.isScope = isScope != 0
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	rules := 0
	scopes := 0
	for _, target := range targets {
		if target.isScope {
			scopes++
		} else {
			rules++
		}
	}
	if dryRun || len(targets) == 0 {
		return rules, scopes, nil
	}

	for _, target := range targets {
		message := "disabled rule metadata pruned"
		if target.isScope {
			message = "disabled scope metadata pruned"
		}
		if err := tx.AddEvent(ctx, &target.ruleID, EventCleanup, message, now); err != nil {
			return 0, 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM xui_factor_rule_clients WHERE rule_id = ?`, target.ruleID); err != nil {
			return 0, 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM xui_factor_scopes WHERE rule_id = ?`, target.ruleID); err != nil {
			return 0, 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM xui_factor_rules WHERE id = ?`, target.ruleID); err != nil {
			return 0, 0, err
		}
	}
	return rules, scopes, nil
}

func (tx *Tx) pruneInactiveExcludes(ctx context.Context, cutoff, now int64, dryRun bool) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM xui_factor_excludes
		WHERE state <> ?
			AND COALESCE(disabled_at, updated_at) <= ?
		ORDER BY id
	`, StateActive, cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if dryRun || len(ids) == 0 {
		return len(ids), nil
	}

	for _, id := range ids {
		message := fmt.Sprintf("exclude policy %d metadata pruned", id)
		if err := tx.AddEvent(ctx, nil, EventCleanup, message, now); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM xui_factor_excludes WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (tx *Tx) pruneInactiveOverrides(ctx context.Context, cutoff, now int64, dryRun bool) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM xui_factor_overrides
		WHERE state <> ?
			AND COALESCE(disabled_at, updated_at) <= ?
		ORDER BY id
	`, StateActive, cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if dryRun || len(ids) == 0 {
		return len(ids), nil
	}

	for _, id := range ids {
		message := fmt.Sprintf("override policy %d metadata pruned", id)
		if err := tx.AddEvent(ctx, nil, EventCleanup, message, now); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM xui_factor_overrides WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (tx *Tx) pruneAuditEvents(ctx context.Context, cutoff int64, dryRun bool) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM xui_factor_events
		WHERE created_at <= ?
	`, cutoff).Scan(&count); err != nil {
		return 0, err
	}
	if dryRun || count == 0 {
		return count, nil
	}
	_, err := tx.ExecContext(ctx, `
		DELETE FROM xui_factor_events
		WHERE created_at <= ?
	`, cutoff)
	if err != nil {
		return 0, err
	}
	return count, nil
}
