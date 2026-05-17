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

func TestEnableRejectsAmbiguousEmailSelection(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: 10, email: "same@example.com", up: 100, down: 200, allTime: 300},
		{id: 2, inboundID: 11, email: "same@example.com", up: 400, down: 500, allTime: 900},
	})
	defer st.Close()

	_, err := svc.Enable(ctx, EnableRequest{Email: "same@example.com", Factor: "1.2"})
	if err == nil || !strings.Contains(err.Error(), "multiple matches") {
		t.Fatalf("expected ambiguous match error, got %v", err)
	}
}

func TestEnableRejectsMissingClient(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: 10, email: "user@example.com", up: 100, down: 200, allTime: 300},
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
		{id: 1, inboundID: 10, email: "user@example.com", up: 100, down: 200, allTime: 300},
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
	var trafficID, lastUp, lastDown, lastAllTime int64
	err = db.QueryRow(`
		SELECT traffic_id, last_up, last_down, last_all_time
		FROM xui_factor_rule_clients
		WHERE rule_id=?
	`, rule.ID).Scan(&trafficID, &lastUp, &lastDown, &lastAllTime)
	if err != nil {
		t.Fatalf("read rule client: %v", err)
	}
	if trafficID != 1 || lastUp != 100 || lastDown != 200 || lastAllTime != 300 {
		t.Fatalf("unexpected baseline: traffic=%d up=%d down=%d all_time=%d", trafficID, lastUp, lastDown, lastAllTime)
	}
}

func TestExcludeLifecycleUsesExactClientIdentity(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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

func TestExcludeSameEmailDifferentTrafficIsDifferentClient(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
	})
	defer st.Close()

	first, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("first exclude: %v", err)
	}
	db := openTestDB(t, dbPath)
	execEngineTestSQL(t, db, `DELETE FROM client_traffics WHERE id=1`)
	execEngineTestSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
		VALUES(2, ?, 1, 'user@example.com', 500, 600, 1100, 0, 0, 0, 0)
	`, inboundID)
	db.Close()

	second, err := svc.Exclude(ctx, ExcludeRequest{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("second exclude: %v", err)
	}
	if first.ID == second.ID || first.TrafficID == second.TrafficID {
		t.Fatalf("first=%#v second=%#v, want separate policies", first, second)
	}
	policies, err := svc.Excludes(ctx, ExcludeListRequest{})
	if err != nil {
		t.Fatalf("list excludes: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("policies = %#v, want two active policies", policies)
	}
}

func TestOverrideLifecycleUsesExactClientIdentity(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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

func TestOverrideSameEmailDifferentTrafficIsDifferentClient(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
	})
	defer st.Close()

	first, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.2"})
	if err != nil {
		t.Fatalf("first override: %v", err)
	}
	db := openTestDB(t, dbPath)
	execEngineTestSQL(t, db, `DELETE FROM client_traffics WHERE id=1`)
	execEngineTestSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
		VALUES(2, ?, 1, 'user@example.com', 500, 600, 1100, 0, 0, 0, 0)
	`, inboundID)
	db.Close()

	second, err := svc.Override(ctx, OverrideRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "1.3"})
	if err != nil {
		t.Fatalf("second override: %v", err)
	}
	if first.ID == second.ID || first.TrafficID == second.TrafficID {
		t.Fatalf("first=%#v second=%#v, want separate policies", first, second)
	}
	policies, err := svc.Overrides(ctx, OverrideListRequest{})
	if err != nil {
		t.Fatalf("list overrides: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("policies = %#v, want two active policies", policies)
	}
}

func TestExplainShowsExcludeWinningOverOverrideAndScope(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "same@example.com", up: 100, down: 200, allTime: 300},
		{id: 2, inboundID: 11, email: "same@example.com", up: 400, down: 500, allTime: 900},
	})
	defer st.Close()

	if _, err := svc.EnableAll(ctx, EnableAllRequest{InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable all: %v", err)
	}
	if _, err := svc.Override(ctx, OverrideRequest{Email: "same@example.com", InboundID: &inboundID, Factor: "1.2"}); err != nil {
		t.Fatalf("override: %v", err)
	}

	first, err := svc.Explain(ctx, ExplainRequest{Email: "same@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("explain first: %v", err)
	}
	otherInbound := int64(11)
	second, err := svc.Explain(ctx, ExplainRequest{Email: "same@example.com", InboundID: &otherInbound})
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
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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
		{id: 1, inboundID: 10, email: "user@example.com", up: 100, down: 200, allTime: 300},
	})
	defer st.Close()

	if _, err := svc.Enable(ctx, EnableRequest{Email: "user@example.com", Factor: "2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	execEngineTestSQL(t, db, `UPDATE client_traffics SET up=150, down=250, all_time=400 WHERE id=1`)

	rule, err := svc.Disable(ctx, RuleSelector{Email: "user@example.com"})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if rule.State != store.StateDisabled {
		t.Fatalf("state = %s, want disabled", rule.State)
	}

	var up, down, allTime int64
	err = db.QueryRow(`SELECT up, down, all_time FROM client_traffics WHERE id=1`).Scan(&up, &down, &allTime)
	if err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if up != 150 || down != 250 || allTime != 400 {
		t.Fatalf("disable changed counters: up=%d down=%d all_time=%d", up, down, allTime)
	}
}

func TestPauseResumeLifecycle(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, dbPath := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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
	execEngineTestSQL(t, db, `UPDATE client_traffics SET up=333, down=444, all_time=777 WHERE id=1`)
	execEngineTestSQL(t, db, `UPDATE xui_factor_rule_clients SET rem_up=9, rem_down=8, rem_all_time=7, missing_since=123 WHERE rule_id=?`, rule.ID)

	resumed, err := svc.Resume(ctx, RuleSelector{Email: "user@example.com", InboundID: &inboundID})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != store.StateActive {
		t.Fatalf("state = %s, want active", resumed.State)
	}

	var lastUp, lastDown, lastAllTime, remUp, remDown, remAllTime int64
	var missingSince sql.NullInt64
	err = db.QueryRow(`
		SELECT last_up, last_down, last_all_time, rem_up, rem_down, rem_all_time, missing_since
		FROM xui_factor_rule_clients
		WHERE rule_id=?
	`, rule.ID).Scan(&lastUp, &lastDown, &lastAllTime, &remUp, &remDown, &remAllTime, &missingSince)
	if err != nil {
		t.Fatalf("read refreshed baseline: %v", err)
	}
	if lastUp != 333 || lastDown != 444 || lastAllTime != 777 {
		t.Fatalf("resume did not refresh baseline: up=%d down=%d all_time=%d", lastUp, lastDown, lastAllTime)
	}
	if remUp != 0 || remDown != 0 || remAllTime != 0 || missingSince.Valid {
		t.Fatalf("resume did not clear remainder state")
	}
}

func TestEnableRejectsActiveTrafficConflict(t *testing.T) {
	ctx := context.Background()
	inboundID := int64(10)
	svc, st, _ := newTestService(t, []testTraffic{
		{id: 1, inboundID: inboundID, email: "user@example.com", up: 100, down: 200, allTime: 300},
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

type testTraffic struct {
	id        int64
	inboundID int64
	email     string
	up        int64
	down      int64
	allTime   int64
}

func newTestService(t *testing.T, traffics []testTraffic) (*Service, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createEngineTestSchema(t, dbPath)

	db := openTestDB(t, dbPath)
	for _, traffic := range traffics {
		execEngineTestSQL(t, db, `
			INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
			VALUES(?, ?, 1, ?, ?, ?, ?, 0, 0, 0, 0)
		`, traffic.id, traffic.inboundID, traffic.email, traffic.up, traffic.down, traffic.allTime)
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

func createEngineTestSchema(t *testing.T, dbPath string) {
	t.Helper()
	db := openTestDB(t, dbPath)
	defer db.Close()
	execEngineTestSQL(t, db, `CREATE TABLE inbounds (id INTEGER PRIMARY KEY)`)
	execEngineTestSQL(t, db, `
		CREATE TABLE client_traffics (
			id INTEGER,
			inbound_id INTEGER,
			enable numeric,
			email TEXT,
			up INTEGER,
			down INTEGER,
			all_time INTEGER,
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
