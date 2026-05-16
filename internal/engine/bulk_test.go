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

func setBulkCounters(t *testing.T, dbPath string, id, up, down, allTime int64) {
	t.Helper()
	db := openBulkDB(t, dbPath)
	defer db.Close()
	execBulkSQL(t, db, `UPDATE client_traffics SET up=?, down=?, all_time=? WHERE id=?`, up, down, allTime, id)
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
