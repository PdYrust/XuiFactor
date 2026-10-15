package engine

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PdYrust/XuiFactor/internal/policy"
	"github.com/PdYrust/XuiFactor/internal/store"
)

func TestEnableSelectsClientByInboundMembership(t *testing.T) {
	ctx := context.Background()
	inboundOne := int64(10)
	inboundTwo := int64(11)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundOne, email: "one@example.com", up: 100, down: 200},
		{id: 2, inboundID: inboundTwo, email: "two@example.com", up: 400, down: 500},
	})
	defer st.Close()

	_, err := svc.Enable(ctx, EnableRequest{Email: "two@example.com", InboundID: &inboundOne, Factor: "1.2"})
	if err == nil || !strings.Contains(err.Error(), "no matching record") {
		t.Fatalf("expected membership miss for inbound 10, got %v", err)
	}
	rule, err := svc.Enable(ctx, EnableRequest{Email: "two@example.com", InboundID: &inboundTwo, Factor: "1.2"})
	if err != nil {
		t.Fatalf("enable by membership: %v", err)
	}
	if rule.InboundID != inboundTwo {
		t.Fatalf("rule inbound = %d, want requested %d", rule.InboundID, inboundTwo)
	}
}

func TestInboundSelectionIgnoresStaleTrafficPointer(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	db := openTestDB(t, dbPath)
	execEngineTestSQL(t, db, `UPDATE client_traffics SET inbound_id=99 WHERE id=1`)
	db.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.2"}); err != nil {
		t.Fatalf("enable through membership: %v", err)
	}
	if _, err := svc.Disable(ctx, RuleSelector{Email: "user@example.com", InboundID: &inboundID}); err != nil {
		t.Fatalf("disable through membership: %v", err)
	}
}

func TestEnableRejectsMissingClient(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: 10, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	_, err := svc.Enable(ctx, EnableRequest{Email: "missing@example.com", Factor: "1.2"})
	if err == nil || !strings.Contains(err.Error(), "no matching record") {
		t.Fatalf("expected missing client error, got %v", err)
	}
}

func TestEnableLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: 10, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	rule, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", Factor: "1.2", Name: "test"})
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if rule.State != store.StateActive {
		t.Fatalf("state = %s, want active", rule.State)
	}
	if rule.FactorPPM != 1_200_000 {
		t.Fatalf("factor = %d, want 1200000", rule.FactorPPM)
	}

	rules, err := svc.Status(ctx, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(rules) != 1 || rules[0].ClientCount != 1 {
		t.Fatalf("unexpected status: %#v", rules)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	var trafficID, lastUp, lastDown int64
	err = db.QueryRow(`
		SELECT traffic_id, last_up, last_down
		FROM xui_factor_rule_clients
		WHERE rule_id=?
	`, rule.ID).Scan(&trafficID, &lastUp, &lastDown)
	if err != nil {
		t.Fatalf("read rule client: %v", err)
	}
	if trafficID != 1 || lastUp != 100 || lastDown != 200 {
		t.Fatalf("unexpected baseline: traffic=%d up=%d down=%d", trafficID, lastUp, lastDown)
	}
}

func TestExcludeLifecycleUsesExactClientIdentity(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	policy, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID, Note: "maintenance"})
	if err != nil {
		t.Fatalf("exclude: %v", err)
	}
	if policy.State != store.StateActive || policy.TrafficID != 1 || policy.InboundID != inboundID || policy.Email != "user@example.com" {
		t.Fatalf("policy = %#v, want active exact identity", policy)
	}

	policies, err := svc.Excludes(ctx, ExcludeListRequest{})
	if err != nil {
		t.Fatalf("list excludes: %v", err)
	}
	if len(policies) != 1 || policies[0].Note != "maintenance" {
		t.Fatalf("policies = %#v, want one active policy", policies)
	}

	disabled, err := svc.Unexclude(ctx, ExcludeSelector{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("unexclude: %v", err)
	}
	if disabled.State != store.StateDisabled {
		t.Fatalf("state = %s, want disabled", disabled.State)
	}
	policies, err = svc.Excludes(ctx, ExcludeListRequest{IncludeInactive: true})
	if err != nil {
		t.Fatalf("list all excludes: %v", err)
	}
	if len(policies) != 1 || policies[0].State != store.StateDisabled {
		t.Fatalf("policies = %#v, want one inactive policy", policies)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	if got := countEngineEvents(t, db, store.EventExcludeEnable); got != 1 {
		t.Fatalf("exclude enable events = %d, want 1", got)
	}
	if got := countEngineEvents(t, db, store.EventExcludeDisable); got != 1 {
		t.Fatalf("exclude disable events = %d, want 1", got)
	}
}

func TestExcludeUpsertDoesNotDuplicateAcrossPointerDrift(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	driftedInbound := int64(99)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	// A disabled row left behind by an older inbound pointer.
	db := openTestDB(t, dbPath)
	execEngineTestSQL(t, db, `
		INSERT INTO xui_factor_excludes(id, traffic_id, inbound_id, email, state, created_at, updated_at, activated_at)
		VALUES(1, 1, 55, 'user@example.com', 'disabled', 50, 50, 50)
	`)
	execEngineTestSQL(t, db, `
		INSERT INTO xui_factor_overrides(id, traffic_id, inbound_id, email, factor_ppm, state, created_at, updated_at, activated_at)
		VALUES(1, 1, 55, 'user@example.com', 1100000, 'disabled', 50, 50, 50)
	`)
	db.Close()

	first, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("first exclude: %v", err)
	}
	if _, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.2"}); err != nil {
		t.Fatalf("first override: %v", err)
	}

	db = openTestDB(t, dbPath)
	execEngineTestSQL(t, db, `UPDATE client_traffics SET inbound_id=99 WHERE id=1`)
	execEngineTestSQL(t, db, `DELETE FROM client_inbounds WHERE client_id=1`)
	execEngineTestSQL(t, db, `INSERT INTO client_inbounds(client_id, inbound_id) VALUES(1, 99)`)
	db.Close()

	second, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &driftedInbound})
	if err != nil {
		t.Fatalf("second exclude: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate exclude rows created: first=%d second=%d", first.ID, second.ID)
	}
	if second.InboundID != driftedInbound {
		t.Fatalf("exclude inbound = %d, want adopted %d", second.InboundID, driftedInbound)
	}

	updated, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &driftedInbound, Factor: "2"})
	if err != nil {
		t.Fatalf("second override: %v", err)
	}
	if updated.InboundID != driftedInbound || updated.FactorPPM != 2_000_000 {
		t.Fatalf("override = %#v, want re-targeted row with factor 2", updated)
	}

	policies, err := svc.Excludes(ctx, ExcludeListRequest{})
	if err != nil {
		t.Fatalf("list excludes: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("active excludes = %d, want 1", len(policies))
	}
	overrides, err := svc.Overrides(ctx, OverrideListRequest{})
	if err != nil {
		t.Fatalf("list overrides: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("active overrides = %d, want 1", len(overrides))
	}

	if _, err := svc.Unexclude(ctx, ExcludeSelector{Email: "user@example.com", InboundID: &driftedInbound}); err != nil {
		t.Fatalf("unexclude through membership: %v", err)
	}
	if _, err := svc.RemoveOverride(ctx, OverrideSelector{Email: "user@example.com", InboundID: &driftedInbound}); err != nil {
		t.Fatalf("remove override through membership: %v", err)
	}
}

func TestStatusAndReportFollowMembershipNotInboundPointer(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	db := openTestDB(t, dbPath)
	execEngineTestSQL(t, db, `UPDATE client_traffics SET inbound_id=99 WHERE id=1`)
	db.Close()

	rules, err := svc.Status(ctx, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(rules) != 1 || rules[0].EffectiveClientCount != 1 {
		t.Fatalf("rules = %#v, want one rule with one effective client", rules)
	}

	report, err := svc.Report(ctx, ReportRequest{InboundID: &inboundID})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.TotalActiveRuleClients != 1 || report.EffectiveFactoredClients != 1 {
		t.Fatalf("report = active clients %d factored %d, want 1 and 1", report.TotalActiveRuleClients, report.EffectiveFactoredClients)
	}
}

func TestOverrideLifecycleUsesExactClientIdentity(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	policy, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.2", Note: "trial"})
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if policy.State != store.StateActive || policy.TrafficID != 1 || policy.InboundID != inboundID || policy.Email != "user@example.com" || policy.FactorPPM != 1_200_000 {
		t.Fatalf("policy = %#v, want active exact identity", policy)
	}

	updated, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.5", Note: "updated"})
	if err != nil {
		t.Fatalf("update override: %v", err)
	}
	if updated.ID != policy.ID || updated.FactorPPM != 1_500_000 {
		t.Fatalf("updated policy = %#v, want same policy with new factor", updated)
	}

	policies, err := svc.Overrides(ctx, OverrideListRequest{})
	if err != nil {
		t.Fatalf("list overrides: %v", err)
	}
	if len(policies) != 1 || policies[0].Note != "updated" {
		t.Fatalf("policies = %#v, want one active override", policies)
	}

	disabled, err := svc.RemoveOverride(ctx, OverrideSelector{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("remove override: %v", err)
	}
	if disabled.State != store.StateDisabled {
		t.Fatalf("state = %s, want disabled", disabled.State)
	}
	policies, err = svc.Overrides(ctx, OverrideListRequest{IncludeInactive: true})
	if err != nil {
		t.Fatalf("list all overrides: %v", err)
	}
	if len(policies) != 1 || policies[0].State != store.StateDisabled {
		t.Fatalf("policies = %#v, want one inactive override", policies)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	if got := countEngineEvents(t, db, store.EventOverrideEnable); got != 1 {
		t.Fatalf("override enable events = %d, want 1", got)
	}
	if got := countEngineEvents(t, db, store.EventOverrideUpdate); got != 1 {
		t.Fatalf("override update events = %d, want 1", got)
	}
	if got := countEngineEvents(t, db, store.EventOverrideDisable); got != 1 {
		t.Fatalf("override disable events = %d, want 1", got)
	}
}

func TestExplainShowsExcludeWinningOverOverrideAndScope(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if _, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.2"}); err != nil {
		t.Fatalf("override: %v", err)
	}
	if _, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID}); err != nil {
		t.Fatalf("exclude: %v", err)
	}

	result, err := svc.Explain(ctx, ExplainRequest{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if result.Client.ID != 1 || result.Decision.SourceType != policy.SourceExclude || result.Decision.FactorPPM != 0 {
		t.Fatalf("decision = %#v, want exclude winner for traffic 1", result.Decision)
	}
	if result.Decision.Target != nil {
		t.Fatalf("target = %#v, want no mutation target for exclude", result.Decision.Target)
	}
	if len(result.Decision.Matched) != 3 {
		t.Fatalf("matched = %d, want exclude override scope", len(result.Decision.Matched))
	}
}

func TestExplainShowsOverrideWinningOverScope(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if _, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.2"}); err != nil {
		t.Fatalf("override: %v", err)
	}

	result, err := svc.Explain(ctx, ExplainRequest{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if result.Decision.SourceType != policy.SourceUserOverride || result.Decision.FactorPPM != 1_200_000 || result.Decision.Target == nil {
		t.Fatalf("decision = %#v, want override winner with materialized target", result.Decision)
	}
	if result.Baseline == nil || result.Baseline.TrafficID != 1 {
		t.Fatalf("baseline = %#v, want current rule-client baseline", result.Baseline)
	}
}

func TestExplainShowsScopeWinningWithoutPolicy(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}

	result, err := svc.Explain(ctx, ExplainRequest{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if result.Decision.SourceType != policy.SourceInboundScope || result.Decision.FactorPPM != 2_000_000 || result.Decision.Target == nil {
		t.Fatalf("decision = %#v, want inbound scope winner", result.Decision)
	}
}

func TestExplainShowsNoFactorWithoutMatchingMetadata(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	result, err := svc.Explain(ctx, ExplainRequest{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if result.Decision.SourceType != policy.SourceNone || result.Decision.Target != nil || len(result.Decision.Matched) != 0 {
		t.Fatalf("decision = %#v, want no factor", result.Decision)
	}
}

func TestExplainUsesExactTrafficIdentity(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	otherInbound := int64(11)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "one@example.com", up: 100, down: 200},
		{id: 2, inboundID: otherInbound, email: "two@example.com", up: 400, down: 500},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if _, err := svc.Override(ctx, OverrideRequest{Email: "one@example.com", InboundID: &inboundID, Factor: "1.2"}); err != nil {
		t.Fatalf("override: %v", err)
	}

	first, err := svc.Explain(ctx, ExplainRequest{Email: "one@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("explain first: %v", err)
	}
	second, err := svc.Explain(ctx, ExplainRequest{Email: "two@example.com", InboundID: &otherInbound})
	if err != nil {
		t.Fatalf("explain second: %v", err)
	}
	if first.Client.ID != 1 || first.Decision.SourceType != policy.SourceUserOverride {
		t.Fatalf("first = %#v, want traffic 1 override", first)
	}
	if second.Client.ID != 2 || second.Decision.SourceType != policy.SourceNone {
		t.Fatalf("second = %#v, want traffic 2 no factor", second)
	}
}

func TestExplainIsReadOnly(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	db := openTestDB(t, dbPath)
	before := countAllEngineEvents(t, db)
	db.Close()
	if _, err := svc.Explain(ctx, ExplainRequest{Email: "user@example.com", InboundID: &inboundID}); err != nil {
		t.Fatalf("explain: %v", err)
	}
	db = openTestDB(t, dbPath)
	after := countAllEngineEvents(t, db)
	db.Close()
	if after != before {
		t.Fatalf("event count after explain = %d, want %d", after, before)
	}
}

func TestDisableKeepsTrafficCounters(t *testing.T) {
	ctx := context.Background()
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: 10, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	execEngineTestSQL(t, db, `UPDATE client_traffics SET up=150, down=250 WHERE id=1`)

	rule, err := svc.Disable(ctx, RuleSelector{Email: "user@example.com"})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if rule.State != store.StateDisabled {
		t.Fatalf("state = %s, want disabled", rule.State)
	}

	var up, down int64
	err = db.QueryRow(`SELECT up, down FROM client_traffics WHERE id=1`).Scan(&up, &down)
	if err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if up != 150 || down != 250 {
		t.Fatalf("disable changed counters: up=%d down=%d", up, down)
	}
}

func TestPauseResumeLifecycle(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	rule, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "3"})
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	paused, err := svc.Pause(ctx, RuleSelector{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if paused.State != store.StatePaused {
		t.Fatalf("state = %s, want paused", paused.State)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	execEngineTestSQL(t, db, `UPDATE client_traffics SET up=333, down=444 WHERE id=1`)
	execEngineTestSQL(t, db, `UPDATE xui_factor_rule_clients SET rem_up=9, rem_down=8, missing_since=123 WHERE rule_id=?`, rule.ID)

	resumed, err := svc.Resume(ctx, RuleSelector{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != store.StateActive {
		t.Fatalf("state = %s, want active", resumed.State)
	}

	var lastUp, lastDown, remUp, remDown int64
	var missingSince sql.NullInt64
	err = db.QueryRow(`
		SELECT last_up, last_down, rem_up, rem_down, missing_since
		FROM xui_factor_rule_clients
		WHERE rule_id=?
	`, rule.ID).Scan(&lastUp, &lastDown, &remUp, &remDown, &missingSince)
	if err != nil {
		t.Fatalf("read refreshed baseline: %v", err)
	}
	if lastUp != 333 || lastDown != 444 {
		t.Fatalf("resume did not refresh baseline: up=%d down=%d", lastUp, lastDown)
	}
	if remUp != 0 || remDown != 0 || missingSince.Valid {
		t.Fatalf("resume did not clear remainder state")
	}
}

func TestEnableRejectsActiveTrafficConflict(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	_, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "3"})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestReportSummarizesActivePoliciesAndEffectiveClients(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "free@example.com", up: 100, down: 200},
		{id: 2, inboundID: inboundID, email: "override@example.com", up: 400, down: 500},
		{id: 3, inboundID: inboundID, email: "scope@example.com", up: 1000, down: 2000},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if _, err := svc.Exclude(ctx, ExcludeRequest{Email: "free@example.com", InboundID: &inboundID}); err != nil {
		t.Fatalf("exclude: %v", err)
	}
	if _, err := svc.Override(ctx, OverrideRequest{Email: "override@example.com", InboundID: &inboundID, Factor: "1.2"}); err != nil {
		t.Fatalf("override: %v", err)
	}

	report, err := svc.Report(ctx, ReportRequest{InboundID: &inboundID})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !report.MetadataReady {
		t.Fatalf("metadata ready = false")
	}
	if report.ActiveScopes != 1 || report.ActiveExcludes != 1 || report.ActiveOverrides != 1 || report.ActiveSingleUserRules != 0 {
		t.Fatalf("policy counts = scopes=%d excludes=%d overrides=%d singles=%d", report.ActiveScopes, report.ActiveExcludes, report.ActiveOverrides, report.ActiveSingleUserRules)
	}
	if report.EffectiveFactoredClients != 2 || report.ExcludedClients != 1 || report.OverriddenClients != 1 || report.NoFactorClients != 0 {
		t.Fatalf("effective counts = factored=%d excluded=%d overridden=%d none=%d", report.EffectiveFactoredClients, report.ExcludedClients, report.OverriddenClients, report.NoFactorClients)
	}
	if report.TotalActiveRuleClients != 3 {
		t.Fatalf("active rule clients = %d, want 3", report.TotalActiveRuleClients)
	}
}

func TestReportInboundFilterAndNoRules(t *testing.T) {
	ctx := context.Background()
	inboundOne := int64(1)
	inboundTwo := int64(2)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundOne, email: "one@example.com", up: 100, down: 200},
		{id: 2, inboundID: inboundTwo, email: "two@example.com", up: 400, down: 500},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{InboundID: &inboundOne, Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	report, err := svc.Report(ctx, ReportRequest{InboundID: &inboundTwo})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.ActiveScopes != 0 || report.EffectiveFactoredClients != 0 || report.NoFactorClients != 1 {
		t.Fatalf("inbound filtered report = scopes=%d factored=%d none=%d", report.ActiveScopes, report.EffectiveFactoredClients, report.NoFactorClients)
	}
}

func TestReportIsReadOnlyAndDoesNotMutateCounters(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()
	if _, err := svc.EnableAll(ctx, EnableAllRequest{InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	beforeEvents := countAllEngineEvents(t, db)
	if _, err := svc.Report(ctx, ReportRequest{InboundID: &inboundID}); err != nil {
		t.Fatalf("report: %v", err)
	}
	afterEvents := countAllEngineEvents(t, db)
	if afterEvents != beforeEvents {
		t.Fatalf("report created audit events: before=%d after=%d", beforeEvents, afterEvents)
	}
	var up, down int64
	if err := db.QueryRow(`SELECT up, down FROM client_traffics WHERE id=1`).Scan(&up, &down); err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if up != 100 || down != 200 {
		t.Fatalf("report changed counters: up=%d down=%d", up, down)
	}
}

func TestAuditFiltersAndTrafficImpactAggregation(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(1)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200},
	})
	defer st.Close()
	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	execEngineTestSQL(t, db, `
		INSERT INTO xui_factor_events(rule_id, event_type, message, created_at)
		VALUES(NULL, ?, 'override policy 7 enabled for traffic id 1 inbound 1 email user@example.com factor 1.2', ?)
	`, store.EventOverrideEnable, int64(1_700_000_010))
	execEngineTestSQL(t, db, `
		INSERT INTO xui_factor_events(rule_id, event_type, message, created_at)
		VALUES(1, ?, 'traffic id 1 inbound 1 email user@example.com extra_up=100 extra_down=50 extra_all_time=150', ?)
	`, store.EventTrafficApply, int64(1_700_000_020))
	execEngineTestSQL(t, db, `
		INSERT INTO xui_factor_events(rule_id, event_type, message, created_at)
		VALUES(NULL, ?, 'override policy 8 enabled for traffic id 2 inbound 2 email other@example.com factor 1.2', ?)
	`, store.EventOverrideEnable, int64(1_699_999_000))

	events, err := svc.Audit(ctx, AuditRequest{EventType: store.EventOverrideEnable, Email: "user@example.com", InboundID: &inboundID, Since: time.Hour, Limit: 10})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(events) != 1 || events[0].PolicyID == nil || *events[0].PolicyID != 7 {
		t.Fatalf("filtered events = %#v, want policy 7", events)
	}
	impact, err := st.TrafficImpact(ctx, store.EventFilter{InboundID: &inboundID})
	if err != nil {
		t.Fatalf("traffic impact: %v", err)
	}
	if impact.Applications != 1 || impact.ExtraBytes != 150 || impact.LastAppliedAt == nil || *impact.LastAppliedAt != 1_700_000_020 {
		t.Fatalf("impact = %#v, want one 150-byte application", impact)
	}
}

type testTraffic struct {
	id        int64
	inboundID int64
	email     string
	up        int64
	down      int64
}

func newTestService(t *testing.T, traffics []testTraffic) (*Service, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createEngineTestSchema(t, dbPath)

	db := openTestDB(t, dbPath)
	for _, traffic := range traffics {
		execEngineTestSQL(t, db, `
			INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, expiry_time, total, reset, last_online)
			VALUES(?, ?, 1, ?, ?, ?, 0, 0, 0, 0)
		`, traffic.id, traffic.inboundID, traffic.email, traffic.up, traffic.down)
		insertEngineTestMembership(t, db, traffic.id, traffic.inboundID, traffic.email)
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

func insertEngineTestMembership(t *testing.T, db *sql.DB, clientID, inboundID int64, email string) {
	t.Helper()
	execEngineTestSQL(t, db, `INSERT INTO clients(id, email) VALUES(?, ?)`, clientID, email)
	execEngineTestSQL(t, db, `INSERT INTO client_inbounds(client_id, inbound_id) VALUES(?, ?)`, clientID, inboundID)
}

func createEngineTestSchema(t *testing.T, dbPath string) {
	t.Helper()
	db := openTestDB(t, dbPath)
	defer db.Close()
	execEngineTestSQL(t, db, `CREATE TABLE inbounds (id INTEGER PRIMARY KEY)`)
	execEngineTestSQL(t, db, `CREATE TABLE clients (
		id INTEGER PRIMARY KEY,
		email TEXT
	)`)
	execEngineTestSQL(t, db, `CREATE TABLE client_inbounds (
		client_id INTEGER NOT NULL,
		inbound_id INTEGER NOT NULL
	)`)
	execEngineTestSQL(t, db, `
		CREATE TABLE client_traffics (
			id INTEGER,
			inbound_id INTEGER,
			enable numeric,
			email TEXT UNIQUE,
			up INTEGER,
			down INTEGER,
			expiry_time INTEGER,
			total INTEGER,
			reset INTEGER,
			last_online INTEGER
		)
	`)
}

func openTestDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func execEngineTestSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec SQL: %v", err)
	}
}

func countEngineEvents(t *testing.T, db *sql.DB, eventType string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_events WHERE event_type=?`, eventType).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

func countAllEngineEvents(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_events`).Scan(&count); err != nil {
		t.Fatalf("count all events: %v", err)
	}
	return count
}
