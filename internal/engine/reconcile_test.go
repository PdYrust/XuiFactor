package engine

import (
	"context"
	"testing"

	"github.com/PdYrust/XuiFactor/internal/store"
)

func TestReconcileOrphansActiveSingleRuleWithZeroClients(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "zero@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "zero@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstActiveSingleRuleID(t, dbPath)
	deleteRuleClientsForRule(t, dbPath, ruleID)

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Checked != 1 || result.Orphaned != 1 || result.Reconciled != 1 {
		t.Fatalf("result = %#v, want checked 1 reconciled 1 orphaned 1", result)
	}
	if got := countBulkRules(t, dbPath, store.StateOrphaned); got != 1 {
		t.Fatalf("orphaned rules = %d, want 1", got)
	}
	rules, err := svc.Status(ctx, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("normal status rules = %#v, want empty", rules)
	}
	if got := countBulkEvents(t, dbPath, store.EventRuleReconcile); got != 1 {
		t.Fatalf("reconcile events = %d, want 1", got)
	}
}

func TestReconcileOrphansActiveSingleRuleWithMissingClient(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "missing@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "missing@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstActiveSingleRuleID(t, dbPath)
	deleteBulkTraffic(t, dbPath, 1)

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Orphaned != 1 || countBulkRules(t, dbPath, store.StateOrphaned) != 1 {
		t.Fatalf("result = %#v, want one orphaned rule", result)
	}
	if !bulkRuleClientMissing(t, dbPath, ruleID, 1) {
		t.Fatalf("missing client was not marked missing")
	}
	if got := countBulkEvents(t, dbPath, store.EventClientMissing); got != 1 {
		t.Fatalf("missing events = %d, want 1", got)
	}
}

func TestReconcileOrphansMismatchedIdentityWithoutCounterChange(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "old@example.com", up: 100, down: 200, allTime: 300}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "old@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstActiveSingleRuleID(t, dbPath)
	updateBulkIdentity(t, dbPath, 1, 2, "new@example.com")
	setBulkCounters(t, dbPath, 1, 110, 220, 330)

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Orphaned != 1 || countBulkRules(t, dbPath, store.StateOrphaned) != 1 {
		t.Fatalf("result = %#v, want one orphaned rule", result)
	}
	if !bulkRuleClientMissing(t, dbPath, ruleID, 1) {
		t.Fatalf("mismatched client was not marked missing")
	}
	assertBulkCounters(t, dbPath, 1, 110, 220, 330)
}

func TestReconcileOrphansDisabledSingleRuleByDefault(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "disabled@example.com", up: 100, down: 200, allTime: 300}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "disabled@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	setBulkEnable(t, dbPath, 1, 0)
	setBulkCounters(t, dbPath, 1, 110, 220, 330)

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.DisabledClients != 1 || result.Orphaned != 1 {
		t.Fatalf("result = %#v, want disabled_clients 1 orphaned 1", result)
	}
	if got := countBulkRules(t, dbPath, store.StateOrphaned); got != 1 {
		t.Fatalf("orphaned rules = %d, want 1", got)
	}
	assertBulkCounters(t, dbPath, 1, 110, 220, 330)
}

func TestReconcileConsolidatesCompatibleLegacyRulesIntoScope(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 3, inboundID: 1, enable: 1, email: "scope@example.com"},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID}); err != nil {
		t.Fatalf("create scope: %v", err)
	}
	scopeRuleID := firstScopeRuleID(t, dbPath)
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 1, inboundID: 1, enable: 1, email: "a@example.com", up: 100, down: 200, allTime: 300})
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 2, inboundID: 1, enable: 1, email: "b@example.com", up: 400, down: 500, allTime: 900})
	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable a: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "b@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable b: %v", err)
	}

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Superseded != 2 || result.Reconciled != 2 {
		t.Fatalf("result = %#v, want two superseded rules", result)
	}
	if got := countRuleClientsForRule(t, dbPath, scopeRuleID); got != 3 {
		t.Fatalf("scope clients = %d, want 3", got)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want 1", got)
	}
	if got := countBulkRules(t, dbPath, store.StateMerged); got != 2 {
		t.Fatalf("merged rules = %d, want 2", got)
	}
	assertNoActiveDuplicateTargets(t, dbPath)
}

func TestReconcileDoesNotAdoptIncompatibleScopeTargets(t *testing.T) {
	ctx := context.Background()
	inboundOne := int64(1)
	inboundTwo := int64(2)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "scope@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "factor@example.com"},
		{id: 3, inboundID: 2, enable: 1, email: "inbound@example.com"},
		{id: 4, inboundID: 1, enable: 1, email: "unlimited@example.com", total: 0},
		{id: 5, inboundID: 1, enable: 1, email: "limited@example.com", total: 100},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundOne, LimitedOnly: true}); err != nil {
		t.Fatalf("create limited scope: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "factor@example.com", InboundID: &inboundOne, Factor: "3"}); err != nil {
		t.Fatalf("enable incompatible factor: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "inbound@example.com", InboundID: &inboundTwo, Factor: "2"}); err != nil {
		t.Fatalf("enable incompatible inbound: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "unlimited@example.com", InboundID: &inboundOne, Factor: "2"}); err != nil {
		t.Fatalf("enable unlimited: %v", err)
	}

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Superseded != 0 || result.Orphaned != 0 || countBulkRules(t, dbPath, store.StateMerged) != 0 {
		t.Fatalf("result = %#v, want no incompatible adoption", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 4 {
		t.Fatalf("active rules = %d, want scope plus three effective singles", got)
	}
}

func TestReconcileRealWorldMixedState(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "scope1@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "scope2@example.com"},
		{id: 3, inboundID: 1, enable: 1, email: "scope3@example.com"},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID}); err != nil {
		t.Fatalf("create scope: %v", err)
	}
	scopeRuleID := firstScopeRuleID(t, dbPath)
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 4, inboundID: 1, enable: 1, email: "legacy1@example.com"})
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 5, inboundID: 1, enable: 1, email: "legacy2@example.com"})
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 6, inboundID: 1, enable: 0, email: "disabled@example.com"})
	if _, err := svc.Enable(ctx, EnableRequest{Email: "legacy1@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable legacy1: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "legacy2@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable legacy2: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "disabled@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable disabled: %v", err)
	}
	insertLegacyRuleWithoutClient(t, dbPath, "empty@example.com", 1, 2_000_000)

	result, err := svc.Reconcile(ctx, ReconcileRequest{InboundID: &inboundID})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Superseded != 2 || result.Orphaned != 2 || result.DisabledClients != 1 {
		t.Fatalf("result = %#v, want superseded 2 orphaned 2 disabled 1", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want one active scope", got)
	}
	if got := countRuleClientsForRule(t, dbPath, scopeRuleID); got != 5 {
		t.Fatalf("scope clients = %d, want 5 eligible active clients", got)
	}
	if got := countBulkRules(t, dbPath, store.StateMerged); got != 2 {
		t.Fatalf("merged rules = %d, want 2", got)
	}
	if got := countBulkRules(t, dbPath, store.StateOrphaned); got != 2 {
		t.Fatalf("orphaned rules = %d, want 2", got)
	}
	rules, err := svc.Status(ctx, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(rules) != 1 || rules[0].Scope == nil || rules[0].ClientCount != 5 {
		t.Fatalf("normal status rules = %#v, want one scope with five clients", rules)
	}
	allRules, err := svc.Status(ctx, true)
	if err != nil {
		t.Fatalf("status all: %v", err)
	}
	if len(allRules) < 5 {
		t.Fatalf("status --all rules = %#v, want concise history included", allRules)
	}
	assertNoActiveDuplicateTargets(t, dbPath)
}

func TestStatusHidesActiveSingleAlreadyOwnedByScope(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "owned@example.com"}})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	insertLegacyRuleWithClient(t, dbPath, 1, "owned@example.com", 1, 2_000_000)

	rules, err := svc.Status(ctx, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(rules) != 1 || rules[0].Scope == nil || rules[0].ClientCount != 1 {
		t.Fatalf("normal status rules = %#v, want only effective scope", rules)
	}
}

func TestReconcileDryRunDoesNotMutateMetadata(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "zero@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "zero@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	deleteRuleClientsForRule(t, dbPath, firstActiveSingleRuleID(t, dbPath))

	result, err := svc.Reconcile(ctx, ReconcileRequest{DryRun: true})
	if err != nil {
		t.Fatalf("reconcile dry-run: %v", err)
	}
	if result.Orphaned != 1 {
		t.Fatalf("result = %#v, want one dry-run orphan candidate", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want dry-run to keep active rule", got)
	}
	if got := countBulkEvents(t, dbPath, store.EventRuleReconcile); got != 0 {
		t.Fatalf("reconcile events = %d, want 0 during dry-run", got)
	}
}

func TestCleanupPrunesOrphanedRulesAfterRetention(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "zero@example.com"}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "zero@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstActiveSingleRuleID(t, dbPath)
	deleteRuleClientsForRule(t, dbPath, ruleID)
	if _, err := svc.Reconcile(ctx, ReconcileRequest{}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	setCleanupDisabledAt(t, dbPath, ruleID, 1_699_000_000)

	result, err := svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig(), DryRun: true})
	if err != nil {
		t.Fatalf("cleanup dry-run: %v", err)
	}
	if result.DisabledRulesPruned != 1 {
		t.Fatalf("dry-run disabled rules pruned = %d, want 1", result.DisabledRulesPruned)
	}
	if got := countBulkRules(t, dbPath, store.StateOrphaned); got != 1 {
		t.Fatalf("dry-run orphaned rules = %d, want 1", got)
	}

	result, err = svc.Cleanup(ctx, CleanupRequest{Config: cleanupTestConfig()})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.DisabledRulesPruned != 1 {
		t.Fatalf("disabled rules pruned = %d, want 1", result.DisabledRulesPruned)
	}
	if got := countBulkRules(t, dbPath, store.StateOrphaned); got != 0 {
		t.Fatalf("orphaned rules = %d, want 0 after cleanup", got)
	}
}

func TestEnableAllOnceDoesNotConsolidateLegacySingles(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "single@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "snapshot@example.com"},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "single@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable single: %v", err)
	}
	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID, Once: true})
	if err != nil {
		t.Fatalf("enable all once: %v", err)
	}
	if result.Mode != "snapshot" || result.Adopted != 0 {
		t.Fatalf("result = %#v, want snapshot with no adoption", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 2 {
		t.Fatalf("active rules = %d, want single plus snapshot scope", got)
	}
	if got := countBulkRules(t, dbPath, store.StateMerged); got != 0 {
		t.Fatalf("merged rules = %d, want 0", got)
	}
}

func deleteRuleClientsForRule(t *testing.T, dbPath string, ruleID int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `DELETE FROM xui_factor_rule_clients WHERE rule_id=?`, ruleID)
}

func deleteBulkTraffic(t *testing.T, dbPath string, trafficID int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `DELETE FROM client_traffics WHERE id=?`, trafficID)
}

func setBulkEnable(t *testing.T, dbPath string, trafficID, enable int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `UPDATE client_traffics SET enable=? WHERE id=?`, enable, trafficID)
}

func insertLegacyRuleWithoutClient(t *testing.T, dbPath, email string, inboundID, factorPPM int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	now := int64(1_700_000_000)
	execBulkSQL(t, db, `
		INSERT INTO xui_factor_rules(name, inbound_id, email, factor_ppm, state, created_at, updated_at, activated_at)
		VALUES('', ?, ?, ?, ?, ?, ?, ?)
	`, inboundID, email, factorPPM, store.StateActive, now, now, now)
}

func insertLegacyRuleWithClient(t *testing.T, dbPath string, trafficID int64, email string, inboundID, factorPPM int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	now := int64(1_700_000_000)
	result, err := db.Exec(`
		INSERT INTO xui_factor_rules(name, inbound_id, email, factor_ppm, state, created_at, updated_at, activated_at)
		VALUES('', ?, ?, ?, ?, ?, ?, ?)
	`, inboundID, email, factorPPM, store.StateActive, now, now, now)
	if err != nil {
		t.Fatalf("insert legacy rule: %v", err)
	}
	ruleID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("legacy rule id: %v", err)
	}
	execBulkSQL(t, db, `
		INSERT INTO xui_factor_rule_clients(
			rule_id, traffic_id, inbound_id, email,
			last_up, last_down, last_all_time,
			rem_up, rem_down, rem_all_time, missing_since, updated_at
		)
		SELECT ?, id, inbound_id, email, up, down, all_time, 0, 0, 0, NULL, ?
		FROM client_traffics
		WHERE id=?
	`, ruleID, now, trafficID)
}
