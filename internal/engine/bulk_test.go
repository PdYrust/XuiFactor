package engine

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PdYrust/XuiFactor/internal/store"
)

func TestEnableAllPersistentAllUsersScopeEnrollsExistingClients(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "b@example.com"},
	})
	defer st.Close()

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "1.2"})
	if err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if result.Matched != 2 || result.Changed != 2 || result.SkippedExisting != 0 {
		t.Fatalf("result = %#v, want matched 2 changed 2", result)
	}
	if result.Mode != "persistent" {
		t.Fatalf("mode = %q, want persistent", result.Mode)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want 1", got)
	}
	if got := countBulkRuleClients(t, dbPath); got != 2 {
		t.Fatalf("rule clients = %d, want 2", got)
	}
	if got := countBulkScopes(t, dbPath, false); got != 1 {
		t.Fatalf("persistent scopes = %d, want 1", got)
	}
}

func TestEnableAllLimitedOnlyTargetsTotalGreaterThanZero(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com", total: 0},
		{id: 2, inboundID: 1, enable: 1, email: "b@example.com", total: 100},
		{id: 3, inboundID: 1, enable: 1, email: "c@example.com", total: 1},
	})
	defer st.Close()

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", LimitedOnly: true})
	if err != nil {
		t.Fatalf("enable all limited: %v", err)
	}
	if result.Matched != 2 || result.Changed != 2 {
		t.Fatalf("result = %#v, want matched 2 changed 2", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want 1", got)
	}
	if got := countBulkRuleClients(t, dbPath); got != 2 {
		t.Fatalf("rule clients = %d, want 2", got)
	}
}

func TestEnableAllPersistentInboundScopeEnrollsExistingClients(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(2)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 2, enable: 1, email: "b@example.com"},
		{id: 3, inboundID: 2, enable: 1, email: "c@example.com"},
	})
	defer st.Close()

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("enable all inbound: %v", err)
	}
	if result.Matched != 2 || result.Changed != 2 {
		t.Fatalf("result = %#v, want matched 2 changed 2", result)
	}
	if got := countBulkRulesForInbound(t, dbPath, store.StateActive, 2); got != 1 {
		t.Fatalf("inbound 2 active rules = %d, want 1", got)
	}
	if got := countBulkRuleClients(t, dbPath); got != 2 {
		t.Fatalf("rule clients = %d, want 2", got)
	}
	if got := countBulkScopesForInbound(t, dbPath, 2); got != 1 {
		t.Fatalf("inbound scopes = %d, want 1", got)
	}
}

func TestEnableAllSkipsExistingActiveAndPausedRules(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "b@example.com"},
		{id: 3, inboundID: 1, enable: 1, email: "c@example.com"},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable active: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "b@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable paused target: %v", err)
	}
	if _, err := svc.Pause(ctx, RuleSelector{Email: "b@example.com"}); err != nil {
		t.Fatalf("pause: %v", err)
	}

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "3"})
	if err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if result.Matched != 3 || result.Changed != 1 || result.SkippedExisting != 2 {
		t.Fatalf("result = %#v, want matched 3 changed 1 skipped 2", result)
	}
}

func TestDisableAllDisablesActiveAndPausedWithoutChangingCounters(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com", up: 10, down: 20, allTime: 30},
		{id: 2, inboundID: 1, enable: 1, email: "b@example.com", up: 40, down: 50, allTime: 90},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if _, err := svc.PauseAll(ctx, BulkSelector{}); err != nil {
		t.Fatalf("pause all: %v", err)
	}
	setBulkCounters(t, dbPath, 1, 100, 200, 300)
	setBulkCounters(t, dbPath, 2, 400, 500, 900)

	result, err := svc.DisableAll(ctx, BulkSelector{})
	if err != nil {
		t.Fatalf("disable all: %v", err)
	}
	if result.Matched != 1 || result.Changed != 1 {
		t.Fatalf("result = %#v, want matched 1 changed 1", result)
	}
	if got := countBulkRules(t, dbPath, store.StateDisabled); got != 1 {
		t.Fatalf("disabled rules = %d, want 1", got)
	}
	assertBulkCounters(t, dbPath, 1, 100, 200, 300)
	assertBulkCounters(t, dbPath, 2, 400, 500, 900)
}

func TestPauseAllPausesOnlyActiveRules(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "b@example.com"},
		{id: 3, inboundID: 1, enable: 1, email: "c@example.com"},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}

	result, err := svc.PauseAll(ctx, BulkSelector{})
	if err != nil {
		t.Fatalf("pause all: %v", err)
	}
	if result.Matched != 1 || result.Changed != 1 {
		t.Fatalf("result = %#v, want matched 1 changed 1", result)
	}
	if got := countBulkRules(t, dbPath, store.StatePaused); got != 1 {
		t.Fatalf("paused rules = %d, want 1", got)
	}
}

func TestResumeAllRefreshesBaselines(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com", up: 10, down: 20, allTime: 30},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if _, err := svc.PauseAll(ctx, BulkSelector{}); err != nil {
		t.Fatalf("pause all: %v", err)
	}
	setBulkCounters(t, dbPath, 1, 100, 200, 300)
	setBulkRuleClientState(t, dbPath, 1, 7, 8, 9, 12345)

	result, err := svc.ResumeAll(ctx, BulkSelector{})
	if err != nil {
		t.Fatalf("resume all: %v", err)
	}
	if result.Matched != 1 || result.Changed != 1 || result.Missing != 0 {
		t.Fatalf("result = %#v, want matched 1 changed 1 missing 0", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want 1", got)
	}
	assertBulkRuleClientBaseline(t, dbPath, 1, 100, 200, 300)
}

func TestEnableAllExcludesDisabledClientsByDefault(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 1, enable: 0, email: "b@example.com"},
	})
	defer st.Close()

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2"})
	if err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if result.Matched != 1 || result.Changed != 1 {
		t.Fatalf("result = %#v, want matched 1 changed 1", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want 1", got)
	}
}

func TestEnableAllIncludeDisabledClients(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 1, enable: 0, email: "b@example.com"},
	})
	defer st.Close()

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", IncludeDisabledClients: true})
	if err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if result.Matched != 2 || result.Changed != 2 {
		t.Fatalf("result = %#v, want matched 2 changed 2", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want 1", got)
	}
	if got := countBulkRuleClients(t, dbPath); got != 2 {
		t.Fatalf("rule clients = %d, want 2", got)
	}
}

func TestEnableAllOnceCreatesSnapshotScope(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "b@example.com"},
	})
	defer st.Close()

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", Once: true})
	if err != nil {
		t.Fatalf("enable all once: %v", err)
	}
	if result.Mode != "snapshot" || result.Matched != 2 || result.Changed != 2 {
		t.Fatalf("result = %#v, want snapshot matched 2 changed 2", result)
	}
	if got := countBulkScopes(t, dbPath, true); got != 1 {
		t.Fatalf("snapshot scopes = %d, want 1", got)
	}
	if got := countBulkRuleClients(t, dbPath); got != 2 {
		t.Fatalf("rule clients = %d, want 2", got)
	}
}

func TestEnableAllNoMatchFailsClearly(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(99)
	svc, st, _ := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
	})
	defer st.Close()

	_, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected no-match error, got %v", err)
	}
}

func TestEnableAllConsolidatesIntoExistingPersistentInboundScope(t *testing.T) {
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

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("enable all consolidate: %v", err)
	}
	if result.Adopted != 2 || result.Changed != 0 || result.SkippedExisting != 1 {
		t.Fatalf("result = %#v, want adopted 2 changed 0 skipped 1", result)
	}
	if got := firstScopeRuleID(t, dbPath); got != scopeRuleID {
		t.Fatalf("scope rule id = %d, want existing %d", got, scopeRuleID)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want 1", got)
	}
	if got := countBulkRules(t, dbPath, store.StateMerged); got != 2 {
		t.Fatalf("merged rules = %d, want 2", got)
	}
	if got := countRuleClientsForRule(t, dbPath, scopeRuleID); got != 3 {
		t.Fatalf("scope clients = %d, want 3", got)
	}
	assertNoActiveDuplicateTargets(t, dbPath)
}

func TestEnableAllConsolidatesWhenCreatingPersistentInboundScope(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "b@example.com"},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable a: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "b@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable b: %v", err)
	}

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("enable all consolidate: %v", err)
	}
	if result.Adopted != 2 || result.Changed != 0 {
		t.Fatalf("result = %#v, want adopted 2 changed 0", result)
	}
	if got := countBulkRules(t, dbPath, store.StateActive); got != 1 {
		t.Fatalf("active rules = %d, want 1", got)
	}
	if got := countBulkRules(t, dbPath, store.StateMerged); got != 2 {
		t.Fatalf("merged rules = %d, want 2", got)
	}
	assertNoActiveDuplicateTargets(t, dbPath)
}

func TestConsolidationPreservesBaselinesAndRemainders(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com", up: 100, down: 200, allTime: 300},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	singleRuleID := firstActiveSingleRuleID(t, dbPath)
	setRuleClientDetailed(t, dbPath, singleRuleID, 1, 111, 222, 333, 7, 8, 9)

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if result.Adopted != 1 {
		t.Fatalf("adopted = %d, want 1", result.Adopted)
	}
	scopeRuleID := firstScopeRuleID(t, dbPath)
	assertRuleClientDetailed(t, dbPath, scopeRuleID, 1, 111, 222, 333, 7, 8, 9)
	assertBulkCounters(t, dbPath, 1, 100, 200, 300)
}

func TestConsolidationDoesNotAdoptDisabledClientUnlessIncluded(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 0, email: "disabled@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "enabled@example.com"},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "disabled@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable disabled: %v", err)
	}
	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("enable all default: %v", err)
	}
	if result.Adopted != 0 || countBulkRules(t, dbPath, store.StateMerged) != 0 || countBulkRules(t, dbPath, store.StateOrphaned) != 1 {
		t.Fatalf("disabled client was not reconciled without include flag: result=%#v", result)
	}
}

func TestConsolidationAdoptsDisabledClientWhenIncluded(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 0, email: "disabled@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "enabled@example.com"},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "disabled@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable disabled: %v", err)
	}
	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID, IncludeDisabledClients: true})
	if err != nil {
		t.Fatalf("enable all include disabled: %v", err)
	}
	if result.Adopted != 1 || countBulkRules(t, dbPath, store.StateMerged) != 1 {
		t.Fatalf("disabled client was not adopted with include flag: result=%#v", result)
	}
}

func TestConsolidationLimitedOnlyDoesNotAdoptUnlimitedClient(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "unlimited@example.com", total: 0},
		{id: 2, inboundID: 1, enable: 1, email: "limited@example.com", total: 100},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "unlimited@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable unlimited: %v", err)
	}
	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID, LimitedOnly: true})
	if err != nil {
		t.Fatalf("enable all limited: %v", err)
	}
	if result.Adopted != 0 || countBulkRules(t, dbPath, store.StateMerged) != 0 {
		t.Fatalf("unlimited client was adopted into limited scope: result=%#v", result)
	}
}

func TestConsolidationSkipsMismatchedIdentityAndIncompatibleRules(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "mismatch@example.com"},
		{id: 2, inboundID: 1, enable: 1, email: "factor@example.com"},
		{id: 3, inboundID: 1, enable: 1, email: "inbound@example.com"},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "mismatch@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable mismatch: %v", err)
	}
	mismatchRuleID := firstActiveSingleRuleID(t, dbPath)
	updateBulkIdentity(t, dbPath, 1, 1, "changed@example.com")
	if _, err := svc.Enable(ctx, EnableRequest{Email: "factor@example.com", InboundID: &inboundID, Factor: "3"}); err != nil {
		t.Fatalf("enable factor: %v", err)
	}
	if _, err := svc.Enable(ctx, EnableRequest{Email: "inbound@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable inbound: %v", err)
	}
	corruptRuleInbound(t, dbPath, latestActiveSingleRuleID(t, dbPath), 2)

	result, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if result.Adopted != 0 {
		t.Fatalf("adopted = %d, want 0", result.Adopted)
	}
	if countBulkRules(t, dbPath, store.StateMerged) != 0 {
		t.Fatalf("incompatible rules were merged")
	}
	if !bulkRuleClientMissing(t, dbPath, mismatchRuleID, 1) {
		t.Fatalf("mismatched identity was not marked missing")
	}
	if got := countBulkEvents(t, dbPath, store.EventRuleSkip); got == 0 {
		t.Fatalf("expected skip audit events")
	}
}

func TestStatusIsScopeFocusedAfterConsolidation(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, _ := newBulkService(t, []bulkTraffic{
		{id: 1, inboundID: 1, enable: 1, email: "a@example.com"},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "a@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.EnableAll(ctx, EnableAllRequest{Factor: "2", InboundID: &inboundID}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	rules, err := svc.Status(ctx, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(rules) != 1 || rules[0].Scope == nil || rules[0].ClientCount != 1 {
		t.Fatalf("status rules = %#v, want one scope rule with one client", rules)
	}
}

type bulkTraffic struct {
	id        int64
	inboundID int64
	enable    int64
	email     string
	up        int64
	down      int64
	allTime   int64
	total     int64
}

func newBulkService(t *testing.T, traffics []bulkTraffic) (*Service, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createEngineTestSchema(t, dbPath)

	db := openBulkDB(t, dbPath)
	for _, traffic := range traffics {
		execBulkSQL(t, db, `
			INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
			VALUES(?, ?, ?, ?, ?, ?, ?, 0, ?, 0, 0)
		`, traffic.id, traffic.inboundID, traffic.enable, traffic.email, traffic.up, traffic.down, traffic.allTime, traffic.total)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	st, err := store.Open(ctx, dbPath, time.Second)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.EnsureReady(ctx); err != nil {
		t.Fatalf("ensure ready: %v", err)
	}
	svc := NewWithClock(st, func() time.Time {
		return time.Unix(1_700_000_000, 0)
	})
	return svc, st, dbPath
}

func countBulkRules(t *testing.T, dbPath, state string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_rules WHERE state=?`, state).Scan(&count); err != nil {
		t.Fatalf("count rules: %v", err)
	}
	return count
}

func countBulkRulesForInbound(t *testing.T, dbPath, state string, inboundID int64) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_rules WHERE state=? AND inbound_id=?`, state, inboundID).Scan(&count); err != nil {
		t.Fatalf("count rules by inbound: %v", err)
	}
	return count
}

func countBulkRuleClients(t *testing.T, dbPath string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_rule_clients`).Scan(&count); err != nil {
		t.Fatalf("count rule clients: %v", err)
	}
	return count
}

func countBulkScopes(t *testing.T, dbPath string, once bool) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	onceValue := 0
	if once {
		onceValue = 1
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_scopes WHERE once=?`, onceValue).Scan(&count); err != nil {
		t.Fatalf("count scopes: %v", err)
	}
	return count
}

func countBulkScopesForInbound(t *testing.T, dbPath string, inboundID int64) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_scopes WHERE inbound_id=?`, inboundID).Scan(&count); err != nil {
		t.Fatalf("count scopes by inbound: %v", err)
	}
	return count
}

func firstScopeRuleID(t *testing.T, dbPath string) int64 {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var id int64
	if err := db.QueryRow(`SELECT rule_id FROM xui_factor_scopes ORDER BY rule_id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("read scope rule id: %v", err)
	}
	return id
}

func firstActiveSingleRuleID(t *testing.T, dbPath string) int64 {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var id int64
	if err := db.QueryRow(`
		SELECT r.id
		FROM xui_factor_rules r
		LEFT JOIN xui_factor_scopes s ON s.rule_id = r.id
		WHERE r.state = ? AND s.rule_id IS NULL
		ORDER BY r.id
		LIMIT 1
	`, store.StateActive).Scan(&id); err != nil {
		t.Fatalf("read active single rule id: %v", err)
	}
	return id
}

func latestActiveSingleRuleID(t *testing.T, dbPath string) int64 {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var id int64
	if err := db.QueryRow(`
		SELECT r.id
		FROM xui_factor_rules r
		LEFT JOIN xui_factor_scopes s ON s.rule_id = r.id
		WHERE r.state = ? AND s.rule_id IS NULL
		ORDER BY r.id DESC
		LIMIT 1
	`, store.StateActive).Scan(&id); err != nil {
		t.Fatalf("read latest active single rule id: %v", err)
	}
	return id
}

func countRuleClientsForRule(t *testing.T, dbPath string, ruleID int64) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_rule_clients WHERE rule_id=?`, ruleID).Scan(&count); err != nil {
		t.Fatalf("count rule clients for rule: %v", err)
	}
	return count
}

func assertNoActiveDuplicateTargets(t *testing.T, dbPath string) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM (
			SELECT rc.traffic_id
			FROM xui_factor_rule_clients rc
			INNER JOIN xui_factor_rules r ON r.id = rc.rule_id
			WHERE r.state = ?
			GROUP BY rc.traffic_id
			HAVING COUNT(*) > 1
		)
	`, store.StateActive).Scan(&count); err != nil {
		t.Fatalf("count duplicate active targets: %v", err)
	}
	if count != 0 {
		t.Fatalf("duplicate active targets = %d, want 0", count)
	}
}

func setBulkCounters(t *testing.T, dbPath string, id, up, down, allTime int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `UPDATE client_traffics SET up=?, down=?, all_time=? WHERE id=?`, up, down, allTime, id)
}

func insertBulkTraffic(t *testing.T, dbPath string, traffic bulkTraffic) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
		VALUES(?, ?, ?, ?, ?, ?, ?, 0, ?, 0, 0)
	`, traffic.id, traffic.inboundID, traffic.enable, traffic.email, traffic.up, traffic.down, traffic.allTime, traffic.total)
}

func updateBulkIdentity(t *testing.T, dbPath string, trafficID, inboundID int64, email string) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `UPDATE client_traffics SET inbound_id=?, email=? WHERE id=?`, inboundID, email, trafficID)
}

func corruptRuleInbound(t *testing.T, dbPath string, ruleID, inboundID int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `UPDATE xui_factor_rules SET inbound_id=? WHERE id=?`, inboundID, ruleID)
}

func assertBulkCounters(t *testing.T, dbPath string, id, wantUp, wantDown, wantAllTime int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var up, down, allTime int64
	if err := db.QueryRow(`SELECT up, down, all_time FROM client_traffics WHERE id=?`, id).Scan(&up, &down, &allTime); err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if up != wantUp || down != wantDown || allTime != wantAllTime {
		t.Fatalf("counters = up:%d down:%d all_time:%d, want up:%d down:%d all_time:%d", up, down, allTime, wantUp, wantDown, wantAllTime)
	}
}

func setBulkRuleClientState(t *testing.T, dbPath string, trafficID, remUp, remDown, remAllTime, missingSince int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `
		UPDATE xui_factor_rule_clients
		SET rem_up=?, rem_down=?, rem_all_time=?, missing_since=?
		WHERE traffic_id=?
	`, remUp, remDown, remAllTime, missingSince, trafficID)
}

func setRuleClientDetailed(t *testing.T, dbPath string, ruleID, trafficID, lastUp, lastDown, lastAllTime, remUp, remDown, remAllTime int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `
		UPDATE xui_factor_rule_clients
		SET last_up=?, last_down=?, last_all_time=?,
			rem_up=?, rem_down=?, rem_all_time=?
		WHERE rule_id=? AND traffic_id=?
	`, lastUp, lastDown, lastAllTime, remUp, remDown, remAllTime, ruleID, trafficID)
}

func assertRuleClientDetailed(t *testing.T, dbPath string, ruleID, trafficID, wantUp, wantDown, wantAllTime, wantRemUp, wantRemDown, wantRemAllTime int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var up, down, allTime, remUp, remDown, remAllTime int64
	if err := db.QueryRow(`
		SELECT last_up, last_down, last_all_time, rem_up, rem_down, rem_all_time
		FROM xui_factor_rule_clients
		WHERE rule_id=? AND traffic_id=?
	`, ruleID, trafficID).Scan(&up, &down, &allTime, &remUp, &remDown, &remAllTime); err != nil {
		t.Fatalf("read rule client detail: %v", err)
	}
	if up != wantUp || down != wantDown || allTime != wantAllTime || remUp != wantRemUp || remDown != wantRemDown || remAllTime != wantRemAllTime {
		t.Fatalf("rule client detail up=%d down=%d all_time=%d rem_up=%d rem_down=%d rem_all_time=%d, want up=%d down=%d all_time=%d rem_up=%d rem_down=%d rem_all_time=%d",
			up, down, allTime, remUp, remDown, remAllTime,
			wantUp, wantDown, wantAllTime, wantRemUp, wantRemDown, wantRemAllTime,
		)
	}
}

func bulkRuleClientMissing(t *testing.T, dbPath string, ruleID, trafficID int64) bool {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var missing sql.NullInt64
	if err := db.QueryRow(`SELECT missing_since FROM xui_factor_rule_clients WHERE rule_id=? AND traffic_id=?`, ruleID, trafficID).Scan(&missing); err != nil {
		t.Fatalf("read missing_since: %v", err)
	}
	return missing.Valid
}

func countBulkEvents(t *testing.T, dbPath, eventType string) int {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_events WHERE event_type=?`, eventType).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

func assertBulkRuleClientBaseline(t *testing.T, dbPath string, trafficID, wantUp, wantDown, wantAllTime int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	var up, down, allTime, remUp, remDown, remAllTime int64
	var missingSince sql.NullInt64
	if err := db.QueryRow(`
		SELECT last_up, last_down, last_all_time, rem_up, rem_down, rem_all_time, missing_since
		FROM xui_factor_rule_clients
		WHERE traffic_id=?
	`, trafficID).Scan(&up, &down, &allTime, &remUp, &remDown, &remAllTime, &missingSince); err != nil {
		t.Fatalf("read rule client: %v", err)
	}
	if up != wantUp || down != wantDown || allTime != wantAllTime || remUp != 0 || remDown != 0 || remAllTime != 0 || missingSince.Valid {
		t.Fatalf("unexpected baseline up=%d down=%d all_time=%d rem_up=%d rem_down=%d rem_all_time=%d missing=%v", up, down, allTime, remUp, remDown, remAllTime, missingSince.Valid)
	}
}

func openBulkDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func execBulkSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec SQL: %v", err)
	}
}
