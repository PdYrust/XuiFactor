package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PdYrust/XuiFactor/internal/config"
	"github.com/PdYrust/XuiFactor/internal/engine"
	"github.com/PdYrust/XuiFactor/internal/store"
)

func TestTickAppliesFactorFive(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	setCounters(t, h.dbPath, 1, 110, 220, 330)

	result, err := h.runner.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if result.Applied != 1 {
		t.Fatalf("applied = %d, want 1", result.Applied)
	}
	assertCounters(t, h.dbPath, 1, 150, 300, 450)
	assertRuleClient(t, h.dbPath, 1, ruleClientState{lastUp: 150, lastDown: 300, lastAllTime: 450})
}

func TestTickAppliesFactorOnePointTwoWithRemainders(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "1.2"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	setCounters(t, h.dbPath, 1, 103, 204, 307)
	result, err := h.runner.Tick(ctx)
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if result.Baselined != 1 || result.Applied != 0 {
		t.Fatalf("first result = %#v, want one baseline and no apply", result)
	}
	assertCounters(t, h.dbPath, 1, 103, 204, 307)
	assertRuleClient(t, h.dbPath, 1, ruleClientState{
		lastUp:      103,
		lastDown:    204,
		lastAllTime: 307,
		remUp:       600000,
		remDown:     800000,
	})

	setCounters(t, h.dbPath, 1, 105, 205, 310)
	result, err = h.runner.Tick(ctx)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if result.Applied != 1 {
		t.Fatalf("second applied = %d, want 1", result.Applied)
	}
	assertCounters(t, h.dbPath, 1, 106, 206, 312)
	assertRuleClient(t, h.dbPath, 1, ruleClientState{lastUp: 106, lastDown: 206, lastAllTime: 312})
}

func TestTickFactorOneOnlyUpdatesBaseline(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "1"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	setCounters(t, h.dbPath, 1, 125, 230, 355)

	result, err := h.runner.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if result.Baselined != 1 || result.Applied != 0 {
		t.Fatalf("result = %#v, want one baseline and no apply", result)
	}
	assertCounters(t, h.dbPath, 1, 125, 230, 355)
	assertRuleClient(t, h.dbPath, 1, ruleClientState{lastUp: 125, lastDown: 230, lastAllTime: 355})
}

func TestDisabledRulesDoNotMutateCounters(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := h.engine.Disable(ctx, engine.RuleSelector{Email: "user@example.com"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	setCounters(t, h.dbPath, 1, 110, 220, 330)

	result, err := h.runner.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if result.ActiveClients != 0 {
		t.Fatalf("active clients = %d, want 0", result.ActiveClients)
	}
	assertCounters(t, h.dbPath, 1, 110, 220, 330)
}

func TestPausedRulesDoNotMutateCounters(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := h.engine.Pause(ctx, engine.RuleSelector{Email: "user@example.com"}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	setCounters(t, h.dbPath, 1, 110, 220, 330)

	result, err := h.runner.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if result.ActiveClients != 0 {
		t.Fatalf("active clients = %d, want 0", result.ActiveClients)
	}
	assertCounters(t, h.dbPath, 1, 110, 220, 330)
}

func TestDisableKeepsPreviouslyMultipliedResults(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	setCounters(t, h.dbPath, 1, 110, 220, 330)
	if _, err := h.runner.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	assertCounters(t, h.dbPath, 1, 150, 300, 450)

	if _, err := h.engine.Disable(ctx, engine.RuleSelector{Email: "user@example.com"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	setCounters(t, h.dbPath, 1, 160, 320, 480)
	if _, err := h.runner.Tick(ctx); err != nil {
		t.Fatalf("tick after disable: %v", err)
	}
	assertCounters(t, h.dbPath, 1, 160, 320, 480)
}

func TestCounterDecreaseRebaselinesWithoutMultiplierWrite(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	setRuleClientRemainders(t, h.dbPath, 1, 123, 456, 789)
	setCounters(t, h.dbPath, 1, 90, 210, 300)

	result, err := h.runner.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if result.Rebaselined != 1 || result.Applied != 0 {
		t.Fatalf("result = %#v, want one rebaseline and no apply", result)
	}
	assertCounters(t, h.dbPath, 1, 90, 210, 300)
	assertRuleClient(t, h.dbPath, 1, ruleClientState{lastUp: 90, lastDown: 210, lastAllTime: 300})
	if got := countEvents(t, h.dbPath, store.EventClientReset); got != 1 {
		t.Fatalf("reset events = %d, want 1", got)
	}
}

func TestMissingClientRowIsMarkedAndSkipped(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	deleteClientTraffic(t, h.dbPath, 1)

	result, err := h.runner.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if result.Missing != 1 {
		t.Fatalf("missing = %d, want 1", result.Missing)
	}
	if !ruleClientMissing(t, h.dbPath, 1) {
		t.Fatalf("expected missing_since to be set")
	}
	if got := countEvents(t, h.dbPath, store.EventClientMissing); got != 1 {
		t.Fatalf("missing events = %d, want 1", got)
	}
}

func TestDuplicateActiveTargetsFailSafely(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	insertDuplicateActiveTarget(t, h.dbPath)
	setCounters(t, h.dbPath, 1, 110, 220, 330)

	_, err := h.runner.Tick(ctx)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	assertCounters(t, h.dbPath, 1, 110, 220, 330)
}

func TestCompareAndSwapRaceRollsBackTick(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	setCounters(t, h.dbPath, 1, 110, 220, 330)
	h.runner.beforeCounterUpdate = func(ctx context.Context, tx *store.Tx, _ store.ActiveRuleClient, current store.ClientTraffic) error {
		_, err := tx.ExecContext(ctx, `UPDATE client_traffics SET up = up + 1 WHERE id = ?`, current.ID)
		return err
	}

	_, err := h.runner.Tick(ctx)
	if !errors.Is(err, store.ErrRace) {
		t.Fatalf("expected race error, got %v", err)
	}
	assertCounters(t, h.dbPath, 1, 110, 220, 330)
	assertRuleClient(t, h.dbPath, 1, ruleClientState{lastUp: 100, lastDown: 200, lastAllTime: 300})
}

func TestTickRaceRollsBackAlreadyAppliedTargets(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{
		{id: 1, inboundID: 1, email: "first@example.com", up: 100, down: 200, allTime: 300},
		{id: 2, inboundID: 1, email: "second@example.com", up: 100, down: 200, allTime: 300},
	})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "first@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable first: %v", err)
	}
	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "second@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable second: %v", err)
	}
	setCounters(t, h.dbPath, 1, 110, 220, 330)
	setCounters(t, h.dbPath, 2, 110, 220, 330)
	h.runner.beforeCounterUpdate = func(ctx context.Context, tx *store.Tx, _ store.ActiveRuleClient, current store.ClientTraffic) error {
		if current.ID != 2 {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE client_traffics SET up = up + 1 WHERE id = ?`, current.ID)
		return err
	}

	_, err := h.runner.Tick(ctx)
	if !errors.Is(err, store.ErrRace) {
		t.Fatalf("expected race error, got %v", err)
	}
	if !strings.Contains(err.Error(), "transaction rolled back") {
		t.Fatalf("expected rollback message, got %v", err)
	}
	assertCounters(t, h.dbPath, 1, 110, 220, 330)
	assertCounters(t, h.dbPath, 2, 110, 220, 330)
	assertRuleClient(t, h.dbPath, 1, ruleClientState{lastUp: 100, lastDown: 200, lastAllTime: 300})
	assertRuleClient(t, h.dbPath, 2, ruleClientState{lastUp: 100, lastDown: 200, lastAllTime: 300})
}

func TestRunTickLogsRecoverableFailure(t *testing.T) {
	ctx := context.Background()
	h := newTickHarness(t, []testTraffic{{id: 1, inboundID: 1, email: "user@example.com", up: 100, down: 200, allTime: 300}})
	defer h.store.Close()

	if _, err := h.engine.Enable(ctx, engine.EnableRequest{Email: "user@example.com", Factor: "5"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	insertDuplicateActiveTarget(t, h.dbPath)

	var errOut bytes.Buffer
	h.runner.err = &errOut
	h.runner.runTick(ctx)

	output := errOut.String()
	if !strings.Contains(output, "tick: error") || !strings.Contains(output, "transaction rolled back") {
		t.Fatalf("expected recoverable tick error log, got %q", output)
	}
}

type tickHarness struct {
	runner *Runner
	engine *engine.Service
	store  *store.Store
	dbPath string
}

type testTraffic struct {
	id        int64
	inboundID int64
	email     string
	up        int64
	down      int64
	allTime   int64
}

type ruleClientState struct {
	lastUp      int64
	lastDown    int64
	lastAllTime int64
	remUp       int64
	remDown     int64
	remAllTime  int64
}

func newTickHarness(t *testing.T, traffics []testTraffic) tickHarness {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createTickSchema(t, dbPath)

	db := openTickDB(t, dbPath)
	for _, traffic := range traffics {
		execTickSQL(t, db, `
			INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
			VALUES(?, ?, 1, ?, ?, ?, ?, 0, 0, 0, 0)
		`, traffic.id, traffic.inboundID, traffic.email, traffic.up, traffic.down, traffic.allTime)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	cfg := config.Defaults()
	cfg.DatabasePath = dbPath
	cfg.BusyTimeout = time.Second
	cfg.PollInterval = time.Second

	st, err := store.Open(ctx, dbPath, cfg.BusyTimeout)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.EnsureReady(ctx); err != nil {
		t.Fatalf("ensure ready: %v", err)
	}
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	return tickHarness{
		runner: NewWithClock(st, cfg, nil, nil, now),
		engine: engine.NewWithClock(st, now),
		store:  st,
		dbPath: dbPath,
	}
}

func createTickSchema(t *testing.T, dbPath string) {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	execTickSQL(t, db, `CREATE TABLE inbounds (id INTEGER PRIMARY KEY)`)
	execTickSQL(t, db, `
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

func setCounters(t *testing.T, dbPath string, id, up, down, allTime int64) {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	execTickSQL(t, db, `UPDATE client_traffics SET up=?, down=?, all_time=? WHERE id=?`, up, down, allTime, id)
}

func assertCounters(t *testing.T, dbPath string, id, wantUp, wantDown, wantAllTime int64) {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	var up, down, allTime int64
	if err := db.QueryRow(`SELECT up, down, all_time FROM client_traffics WHERE id=?`, id).Scan(&up, &down, &allTime); err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if up != wantUp || down != wantDown || allTime != wantAllTime {
		t.Fatalf("counters = up:%d down:%d all_time:%d, want up:%d down:%d all_time:%d", up, down, allTime, wantUp, wantDown, wantAllTime)
	}
}

func assertRuleClient(t *testing.T, dbPath string, trafficID int64, want ruleClientState) {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	var got ruleClientState
	if err := db.QueryRow(`
		SELECT last_up, last_down, last_all_time, rem_up, rem_down, rem_all_time
		FROM xui_factor_rule_clients
		WHERE traffic_id=?
	`, trafficID).Scan(&got.lastUp, &got.lastDown, &got.lastAllTime, &got.remUp, &got.remDown, &got.remAllTime); err != nil {
		t.Fatalf("read rule client: %v", err)
	}
	if got != want {
		t.Fatalf("rule client = %#v, want %#v", got, want)
	}
}

func setRuleClientRemainders(t *testing.T, dbPath string, trafficID, remUp, remDown, remAllTime int64) {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	execTickSQL(t, db, `UPDATE xui_factor_rule_clients SET rem_up=?, rem_down=?, rem_all_time=? WHERE traffic_id=?`, remUp, remDown, remAllTime, trafficID)
}

func deleteClientTraffic(t *testing.T, dbPath string, trafficID int64) {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	execTickSQL(t, db, `DELETE FROM client_traffics WHERE id=?`, trafficID)
}

func ruleClientMissing(t *testing.T, dbPath string, trafficID int64) bool {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	var missing sql.NullInt64
	if err := db.QueryRow(`SELECT missing_since FROM xui_factor_rule_clients WHERE traffic_id=?`, trafficID).Scan(&missing); err != nil {
		t.Fatalf("read missing_since: %v", err)
	}
	return missing.Valid
}

func countEvents(t *testing.T, dbPath, eventType string) int {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xui_factor_events WHERE event_type=?`, eventType).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

func insertDuplicateActiveTarget(t *testing.T, dbPath string) {
	t.Helper()
	db := openTickDB(t, dbPath)
	defer db.Close()
	now := int64(1_700_000_000)
	result, err := db.Exec(`
		INSERT INTO xui_factor_rules(name, inbound_id, email, factor_ppm, state, created_at, updated_at, activated_at)
		VALUES('', 1, 'user@example.com', 5000000, 'active', ?, ?, ?)
	`, now, now, now)
	if err != nil {
		t.Fatalf("insert duplicate rule: %v", err)
	}
	ruleID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("duplicate rule id: %v", err)
	}
	execTickSQL(t, db, `
		INSERT INTO xui_factor_rule_clients(rule_id, traffic_id, inbound_id, email, last_up, last_down, last_all_time, updated_at)
		VALUES(?, 1, 1, 'user@example.com', 100, 200, 300, ?)
	`, ruleID, now)
}

func openTickDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func execTickSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec SQL: %v", err)
	}
}
