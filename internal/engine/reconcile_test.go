package engine

import (
	"context"
	"testing"

	"github.com/PdYrust/XuiFactor/internal/policy"
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

func TestReconcileRelinksRuleAfterTrafficRowReplaced(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstActiveSingleRuleID(t, dbPath)

	// The panel replaced the email-keyed traffic row on upgrade or rename churn.
	db := openBulkDB(t, dbPath)
	execBulkSQL(t, db, `DELETE FROM client_traffics WHERE id=1`)
	execBulkSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, expiry_time, total, reset, last_online)
		VALUES(2, 1, 1, 'user@example.com', 500, 600, 0, 0, 0, 0)
	`)
	db.Close()

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Reconciled != 1 || result.Orphaned != 0 {
		t.Fatalf("result = %#v, want one reconciled rule", result)
	}
	if countBulkRules(t, dbPath, store.StateActive) != 1 {
		t.Fatalf("rule no longer active after relink")
	}
	if bulkRuleClientMissing(t, dbPath, ruleID, 2) {
		t.Fatalf("relinked rule client is marked missing")
	}
	assertBulkRuleClientBaseline(t, dbPath, 2, 500, 600)
	if got := countBulkEvents(t, dbPath, store.EventClientRelinked); got != 1 {
		t.Fatalf("relink events = %d, want 1", got)
	}
}

func TestReconcileRevivesMissingRuleClientWhoseRowReturned(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstActiveSingleRuleID(t, dbPath)
	setBulkCounters(t, dbPath, 1, 500, 600)
	setBulkRuleClientState(t, dbPath, 1, 0, 0, 1_699_999_000)

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Reconciled != 1 || result.Orphaned != 0 {
		t.Fatalf("result = %#v, want one reconciled rule", result)
	}
	if bulkRuleClientMissing(t, dbPath, ruleID, 1) {
		t.Fatalf("returned client is still marked missing")
	}
	assertBulkRuleClientBaseline(t, dbPath, 1, 500, 600)
}

func TestReconcileRelinksPoliciesAfterTrafficRowReplaced(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID}); err != nil {
		t.Fatalf("exclude: %v", err)
	}
	if _, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.5"}); err != nil {
		t.Fatalf("override: %v", err)
	}

	// The panel replaced the email-keyed traffic row.
	db := openBulkDB(t, dbPath)
	execBulkSQL(t, db, `DELETE FROM client_traffics WHERE id=1`)
	db.Close()
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 9, inboundID: 1, enable: 1, email: "user@example.com", up: 500, down: 600})

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.PoliciesRepaired != 2 {
		t.Fatalf("policies repaired = %d, want 2", result.PoliciesRepaired)
	}

	db = openBulkDB(t, dbPath)
	defer db.Close()
	var excludeTrafficID, overrideTrafficID int64
	if err := db.QueryRow(`SELECT traffic_id FROM xui_factor_excludes WHERE state='active'`).Scan(&excludeTrafficID); err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	if err := db.QueryRow(`SELECT traffic_id FROM xui_factor_overrides WHERE state='active'`).Scan(&overrideTrafficID); err != nil {
		t.Fatalf("read override: %v", err)
	}
	if excludeTrafficID != 9 || overrideTrafficID != 9 {
		t.Fatalf("policy traffic ids = exclude %d override %d, want both 9", excludeTrafficID, overrideTrafficID)
	}

	explain, err := svc.Explain(ctx, ExplainRequest{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if explain.Decision.SourceType != policy.SourceExclude {
		t.Fatalf("decision = %#v, want the relinked exclude to win", explain.Decision)
	}
}

func TestExcludeReissueMovesStaleRowWithoutReconcile(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	first, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID, Note: "first"})
	if err != nil {
		t.Fatalf("first exclude: %v", err)
	}

	db := openBulkDB(t, dbPath)
	execBulkSQL(t, db, `DELETE FROM client_traffics WHERE id=1`)
	db.Close()
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 9, inboundID: 1, enable: 1, email: "user@example.com", up: 500, down: 600})

	second, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID, Note: "second"})
	if err != nil {
		t.Fatalf("second exclude: %v", err)
	}
	if second.ID != first.ID || second.TrafficID != 9 || second.Note != "second" {
		t.Fatalf("second exclude = %#v, want the moved row %d on traffic 9", second, first.ID)
	}
	if got := countBulkEvents(t, dbPath, store.EventExcludeDisable); got != 0 {
		t.Fatalf("exclude disable events = %d, want 0", got)
	}

	disabled, err := svc.Unexclude(ctx, ExcludeSelector{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("unexclude: %v", err)
	}
	if disabled.State != store.StateDisabled {
		t.Fatalf("state = %s, want disabled", disabled.State)
	}
}

func TestOverrideReissueUpdatesStaleRowWithoutReconcile(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	first, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.5"})
	if err != nil {
		t.Fatalf("first override: %v", err)
	}

	db := openBulkDB(t, dbPath)
	execBulkSQL(t, db, `DELETE FROM client_traffics WHERE id=1`)
	db.Close()
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 9, inboundID: 1, enable: 1, email: "user@example.com", up: 500, down: 600})

	second, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "2"})
	if err != nil {
		t.Fatalf("second override: %v", err)
	}
	if second.ID != first.ID || second.TrafficID != 9 || second.FactorPPM != 2_000_000 {
		t.Fatalf("second override = %#v, want the moved row %d on traffic 9 with factor 2", second, first.ID)
	}

	removed, err := svc.RemoveOverride(ctx, OverrideSelector{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("remove override: %v", err)
	}
	if removed.State != store.StateDisabled {
		t.Fatalf("state = %s, want disabled", removed.State)
	}
}

func TestReconcileRetiresDuplicateActivePolicyRows(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	// The client row was replaced, and a policy row exists for both the old
	// and the current traffic row.
	db := openBulkDB(t, dbPath)
	execBulkSQL(t, db, `DELETE FROM client_traffics WHERE id=1`)
	execBulkSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, expiry_time, total, reset, last_online)
		VALUES(9, 1, 1, 'user@example.com', 100, 200, 0, 0, 0, 0)
	`)
	execBulkSQL(t, db, `INSERT INTO xui_factor_excludes(traffic_id, inbound_id, email, state, created_at, updated_at, activated_at)
		VALUES(1, 1, 'user@example.com', 'active', 100, 100, 100)`)
	execBulkSQL(t, db, `INSERT INTO xui_factor_excludes(traffic_id, inbound_id, email, state, created_at, updated_at, activated_at)
		VALUES(9, 1, 'user@example.com', 'active', 200, 200, 200)`)
	db.Close()

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.PoliciesRepaired != 1 {
		t.Fatalf("policies repaired = %d, want 1", result.PoliciesRepaired)
	}

	policies, err := svc.Excludes(ctx, ExcludeListRequest{})
	if err != nil {
		t.Fatalf("list excludes: %v", err)
	}
	if len(policies) != 1 || policies[0].TrafficID != 9 {
		t.Fatalf("active excludes = %#v, want one row on traffic 9", policies)
	}

	if _, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID, Note: "reissued"}); err != nil {
		t.Fatalf("reissue exclude: %v", err)
	}
	policies, err = svc.Excludes(ctx, ExcludeListRequest{})
	if err != nil {
		t.Fatalf("list excludes after reissue: %v", err)
	}
	if len(policies) != 1 || policies[0].TrafficID != 9 || policies[0].Note != "reissued" {
		t.Fatalf("active excludes after reissue = %#v, want the current row updated", policies)
	}
}

func TestReconcileKeepsActivePolicyBesideDisabledLegacyDuplicate(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	// Legacy shape: an active exclude plus a disabled duplicate from an older
	// inbound pointer, both for the same client row.
	db := openBulkDB(t, dbPath)
	execBulkSQL(t, db, `INSERT INTO xui_factor_excludes(traffic_id, inbound_id, email, state, created_at, updated_at, activated_at)
		VALUES(1, 1, 'user@example.com', 'active', 100, 100, 100)`)
	execBulkSQL(t, db, `INSERT INTO xui_factor_excludes(traffic_id, inbound_id, email, state, created_at, updated_at, activated_at, disabled_at)
		VALUES(1, 9, 'user@example.com', 'disabled', 50, 50, 50, 60)`)
	execBulkSQL(t, db, `UPDATE client_traffics SET inbound_id = 7 WHERE id = 1`)
	db.Close()

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.PoliciesRepaired != 1 {
		t.Fatalf("policies repaired = %d, want 1", result.PoliciesRepaired)
	}

	policies, err := svc.Excludes(ctx, ExcludeListRequest{})
	if err != nil {
		t.Fatalf("list excludes: %v", err)
	}
	if len(policies) != 1 || policies[0].State != store.StateActive || policies[0].InboundID != 7 {
		t.Fatalf("active excludes = %#v, want the active row adopted to inbound 7", policies)
	}

	db = openBulkDB(t, dbPath)
	defer db.Close()
	var disabledInbound int64
	if err := db.QueryRow(`SELECT inbound_id FROM xui_factor_excludes WHERE state='disabled'`).Scan(&disabledInbound); err != nil {
		t.Fatalf("read disabled duplicate: %v", err)
	}
	if disabledInbound != 9 {
		t.Fatalf("disabled duplicate inbound = %d, want it untouched at 9", disabledInbound)
	}
}

func TestReconcileAdoptsDisabledDuplicateAtTargetTuple(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	// The disabled legacy row already occupies the target (traffic, inbound,
	// email) tuple, so reconciliation adopts it with the active row's note.
	db := openBulkDB(t, dbPath)
	execBulkSQL(t, db, `INSERT INTO xui_factor_excludes(traffic_id, inbound_id, email, state, note, created_at, updated_at, activated_at)
		VALUES(1, 1, 'user@example.com', 'active', 'keep-me', 100, 100, 100)`)
	execBulkSQL(t, db, `INSERT INTO xui_factor_excludes(traffic_id, inbound_id, email, state, created_at, updated_at, activated_at, disabled_at)
		VALUES(1, 7, 'user@example.com', 'disabled', 50, 50, 50, 60)`)
	execBulkSQL(t, db, `UPDATE client_traffics SET inbound_id = 7 WHERE id = 1`)
	db.Close()

	result, err := svc.Reconcile(ctx, ReconcileRequest{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.PoliciesRepaired != 1 {
		t.Fatalf("policies repaired = %d, want 1", result.PoliciesRepaired)
	}

	policies, err := svc.Excludes(ctx, ExcludeListRequest{})
	if err != nil {
		t.Fatalf("list excludes: %v", err)
	}
	if len(policies) != 1 || policies[0].State != store.StateActive {
		t.Fatalf("active excludes = %#v, want exactly one active policy", policies)
	}
	if policies[0].ID != 2 || policies[0].InboundID != 7 || policies[0].Note != "keep-me" {
		t.Fatalf("active exclude = %#v, want the adopted row 2 at inbound 7 with the note", policies[0])
	}
}

func TestReconcileOrphansMismatchedIdentityWithoutCounterChange(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "old@example.com", up: 100, down: 200}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "old@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ruleID := firstActiveSingleRuleID(t, dbPath)
	updateBulkIdentity(t, dbPath, 1, 2, "new@example.com")
	setBulkCounters(t, dbPath, 1, 110, 220)

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
	assertBulkCounters(t, dbPath, 1, 110, 220)
}

func TestReconcileOrphansDisabledSingleRuleByDefault(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{{id: 1, inboundID: 1, enable: 1, email: "disabled@example.com", up: 100, down: 200}})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "disabled@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	setBulkEnable(t, dbPath, 1, 0)
	setBulkCounters(t, dbPath, 1, 110, 220)

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
	assertBulkCounters(t, dbPath, 1, 110, 220)
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
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 1, inboundID: 1, enable: 1, email: "a@example.com", up: 100, down: 200})
	insertBulkTraffic(t, dbPath, bulkTraffic{id: 2, inboundID: 1, enable: 1, email: "b@example.com", up: 400, down: 500})
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
			last_up, last_down,
			rem_up, rem_down, missing_since, updated_at
		)
		SELECT ?, id, inbound_id, email, up, down, 0, 0, NULL, ?
		FROM client_traffics
		WHERE id=?
	`, ruleID, now, trafficID)
}
