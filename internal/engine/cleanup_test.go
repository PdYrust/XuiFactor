package engine

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/PdYrust/XuiFactor/internal/config"
	"github.com/PdYrust/XuiFactor/internal/store"
)

func TestCleanupDryRunDoesNotDeleteMetadata(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstCleanupRuleID(t, dbPath)
	setCleanupRuleClientMissing(t, dbPath, ruleID, 1, 1_699_999_000)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig(), DryRun: true})
	if err != nil {
		t.Fatalf("cleanup dry-run: %v", err)
	}
	if result.MissingClientsPruned != 1 {
		t.Fatalf("missing pruned = %d, want 1", result.MissingClientsPruned)
	}
	if got := countCleanupRuleClients(t, dbPath); got != 1 {
		t.Fatalf("rule clients = %d, want 1", got)
	}
}

func TestCleanupPrunesMissingMaterializedClientsAfterGrace(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstCleanupRuleID(t, dbPath)
	setCleanupRuleClientMissing(t, dbPath, ruleID, 1, 1_699_999_000)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.MissingClientsPruned != 1 {
		t.Fatalf("missing pruned = %d, want 1", result.MissingClientsPruned)
	}
	if got := countCleanupRuleClients(t, dbPath); got != 0 {
		t.Fatalf("rule clients = %d, want 0", got)
	}
	if got := countCleanupEvents(t, dbPath, store.EventClientPruned); got != 1 {
		t.Fatalf("client pruned events = %d, want 1", got)
	}
}

func TestCleanupDoesNotPruneMissingBeforeGrace(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstCleanupRuleID(t, dbPath)
	setCleanupRuleClientMissing(t, dbPath, ruleID, 1, 1_699_999_990)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.MissingClientsPruned != 0 {
		t.Fatalf("missing pruned = %d, want 0", result.MissingClientsPruned)
	}
	if got := countCleanupRuleClients(t, dbPath); got != 1 {
		t.Fatalf("rule clients = %d, want 1", got)
	}
}

func TestStatusDoesNotCountMissingMaterializedClients(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstCleanupRuleID(t, dbPath)
	setCleanupRuleClientMissing(t, dbPath, ruleID, 1, 1_699_999_990)

	rules, err := svc.Status(ctx, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(rules) != 1 || rules[0].ClientCount != 0 {
		t.Fatalf("rules = %#v, want one rule with zero counted clients", rules)
	}
}

func TestCleanupPrunesDisabledSingleUserRulesAfterRetention(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com", up: 10, down: 20, allTime: 30}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.Disable(ctx, RuleSelector{Email: "a@example.com"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	setCleanupDisabledAt(t, dbPath, firstCleanupRuleID(t, dbPath), 1_699_000_000)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.DisabledRulesPruned != 1 || result.DisabledScopesPruned != 0 {
		t.Fatalf("result = %#v, want one disabled single-user rule", result)
	}
	if got := countCleanupRules(t, dbPath); got != 0 {
		t.Fatalf("rules = %d, want 0", got)
	}
	assertBulkCounters(t, dbPath, 1, 10, 20, 30)
}

func TestCleanupPrunesMergedRulesAfterRetention(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	mergedRuleID := firstMergedRuleID(t, dbPath)
	setCleanupDisabledAt(t, dbPath, mergedRuleID, 1_699_000_000)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.DisabledRulesPruned != 1 {
		t.Fatalf("disabled rules pruned = %d, want 1", result.DisabledRulesPruned)
	}
	if got := countCleanupRulesInState(t, dbPath, store.StateMerged); got != 0 {
		t.Fatalf("merged rules = %d, want 0", got)
	}
	if got := countCleanupScopes(t, dbPath); got != 1 {
		t.Fatalf("scope rules = %d, want 1", got)
	}
}

func TestCleanupDoesNotRemoveDatabaseFile(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database missing before cleanup: %v", err)
	}
	if _, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig(), Vacuum: true}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database missing after cleanup: %v", err)
	}
}

func TestCleanupPrunesDisabledScopeRulesAfterRetention(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if _, err := svc.DisableAll(ctx, BulkSelector{}); err != nil {
		t.Fatalf("disable all: %v", err)
	}
	setCleanupDisabledAt(t, dbPath, firstCleanupRuleID(t, dbPath), 1_699_000_000)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.DisabledScopesPruned != 1 || result.DisabledRulesPruned != 0 {
		t.Fatalf("result = %#v, want one disabled scope", result)
	}
	if got := countCleanupScopes(t, dbPath); got != 0 {
		t.Fatalf("scopes = %d, want 0", got)
	}
	if got := countCleanupRules(t, dbPath); got != 0 {
		t.Fatalf("rules = %d, want 0", got)
	}
}

func TestCleanupDoesNotPruneActiveOrPausedScopes(t *testing.T) {
	ctx := context.Background()
	inboundOne := int64(1)
	inboundTwo := int64(2)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 2, enable: 1, email: "b@example.com"},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundOne}); err != nil {
		t.Fatalf("enable all active: %v", err)
	}
	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "3", InboundID: &inboundTwo}); err != nil {
		t.Fatalf("enable all paused scope: %v", err)
	}
	if _, err := svc.PauseAll(ctx, BulkSelector{InboundID: &inboundTwo}); err != nil {
		t.Fatalf("pause scoped rule: %v", err)
	}
	setAllCleanupRulesOld(t, dbPath)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.DisabledRulesPruned != 0 || result.DisabledScopesPruned != 0 {
		t.Fatalf("result = %#v, want no active or paused pruning", result)
	}
	if got := countCleanupScopes(t, dbPath); got != 2 {
		t.Fatalf("scopes = %d, want 2", got)
	}
}

func TestCleanupPrunesOldAuditEventsAfterRetention(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()
	insertCleanupEvent(t, dbPath, nil, store.EventCleanup, "old", 1_697_000_000)
	insertCleanupEvent(t, dbPath, nil, store.EventCleanup, "new", 1_699_999_990)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.AuditEventsPruned != 1 {
		t.Fatalf("audit pruned = %d, want 1", result.AuditEventsPruned)
	}
	if got := countAllCleanupEvents(t, dbPath); got != 1 {
		t.Fatalf("events = %d, want 1", got)
	}
}

func TestCleanupVacuumOnlyWhenRequested(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newBulkService(t, nil)
	defer st.Close()

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.VacuumRun {
		t.Fatal("vacuum ran without --vacuum")
	}
	result, err = svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig(), DryRun: true, Vacuum: true})
	if err != nil {
		t.Fatalf("cleanup dry-run vacuum: %v", err)
	}
	if result.VacuumRun {
		t.Fatal("vacuum ran during dry-run")
	}
	result, err = svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig(), Vacuum: true})
	if err != nil {
		t.Fatalf("cleanup vacuum: %v", err)
	}
	if !result.VacuumRun {
		t.Fatal("vacuum did not run when requested")
	}
}

func TestCleanupOlderThanOverridesDisabledAndAuditRetention(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "a@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.Disable(ctx, RuleSelector{Email: "a@example.com"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	setCleanupDisabledAt(t, dbPath, firstCleanupRuleID(t, dbPath), 1_699_996_000)
	insertCleanupEvent(t, dbPath, nil, store.EventCleanup, "old enough for override", 1_699_996_000)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig(), OlderThan: time.Hour})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.DisabledRulesPruned != 1 || result.AuditEventsPruned == 0 {
		t.Fatalf("result = %#v, want override to prune disabled rule and audit event", result)
	}
}

func cleanupTestConfig() config.Config {
	cfg := config.Defaults()
	cfg.MissingClientGrace = 30 * time.Second
	cfg.DisabledRuleRetention = 7 * 24 * time.Hour
	cfg.AuditRetention = 30 * 24 * time.Hour
	return cfg
}

func firstCleanupRuleID(t *testing.T, dbPath string) int64 {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var id int64
	if err := db.QueryRow(`SELECT id FROM xui_factor_rules ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("read rule id: %v", err)
	}
	return id
}

func firstMergedRuleID(t *testing.T, dbPath string) int64 {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var id int64
	if err := db.QueryRow(`SELECT id FROM xui_factor_rules WHERE state=? ORDER BY id LIMIT 1`, store.StateMerged).Scan(&id); err != nil {
		t.Fatalf("read merged rule id: %v", err)
	}
	return id
}

func setCleanupRuleClientMissing(t *testing.T, dbPath string, ruleID, trafficID, missingSince int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `
		UPDATE xui_factor_rule_clients
		SET missing_since=?
		WHERE rule_id=? AND traffic_id=?
	`, missingSince, ruleID, trafficID)
}

func setCleanupDisabledAt(t *testing.T, dbPath string, ruleID, disabledAt int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `UPDATE xui_factor_rules SET disabled_at=?, updated_at=? WHERE id=?`, disabledAt, disabledAt, ruleID)
}

func setAllCleanupRulesOld(t *testing.T, dbPath string) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `UPDATE xui_factor_rules SET disabled_at=1699000000, updated_at=1699000000`)
}

func insertCleanupEvent(t *testing.T, dbPath string, ruleID *int64, eventType, message string, createdAt int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var nullableRuleID sql.NullInt64
	if ruleID != nil {
		nullableRuleID = sql.NullInt64{Int64: *ruleID, Valid: true}
	}
	execBulkSQL(t, db, `
		INSERT INTO xui_factor_events(rule_id, event_type, message, created_at)
		VALUES(?, ?, ?, ?)
	`, nullableRuleID, eventType, message, createdAt)
}

func countCleanupRuleClients(t *testing.T, dbPath string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	return countCleanupRows(t, db, `SELECT COUNT(*) FROM xui_factor_rule_clients`)
}

func countCleanupRules(t *testing.T, dbPath string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	return countCleanupRows(t, db, `SELECT COUNT(*) FROM xui_factor_rules`)
}

func countCleanupRulesInState(t *testing.T, dbPath, state string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_rules WHERE state=?`, state).Scan(&count); err != nil {
		t.Fatalf("count rules in state: %v", err)
	}
	return count
}

func countCleanupScopes(t *testing.T, dbPath string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	return countCleanupRows(t, db, `SELECT COUNT(*) FROM xui_factor_scopes`)
}

func countCleanupEvents(t *testing.T, dbPath, eventType string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_events WHERE event_type=?`, eventType).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

func countAllCleanupEvents(t *testing.T, dbPath string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	return countCleanupRows(t, db, `SELECT COUNT(*) FROM xui_factor_events`)
}

func countCleanupRows(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}
