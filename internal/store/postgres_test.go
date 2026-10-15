package store

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Postgres integration tests run only when TEST_POSTGRES_DSN points at a
// throwaway database: the helpers drop and recreate every table they touch.
// Test binaries from different packages share one database, so a session
// advisory lock serializes them.

const postgresTestLockKey = int64(0x58554946_54455354) // "XUIFTEST"

var postgresTestTables = []string{
	"client_traffics",
	"client_inbounds",
	"clients",
	"inbounds",
	"xui_factor_rule_clients",
	"xui_factor_rules",
	"xui_factor_scopes",
	"xui_factor_excludes",
	"xui_factor_overrides",
	"xui_factor_events",
	"xui_factor_meta",
}

func requirePostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set; skipping postgres integration tests")
	}
	return dsn
}

func openPostgresTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := requirePostgresDSN(t)
	ctx := context.Background()

	// Hold a session lock so parallel test packages never reset the schema
	// while another package is using it.
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open raw postgres connection: %v", err)
	}
	lockConn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve postgres lock connection: %v", err)
	}
	if _, err := lockConn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", postgresTestLockKey); err != nil {
		t.Fatalf("acquire postgres test lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", postgresTestLockKey)
		lockConn.Close()
		raw.Close()
	})

	st, err := OpenPostgres(ctx, dsn, 5*time.Second)
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	resetPostgresTestSchema(t, ctx, st)
	return st, ctx
}

func resetPostgresTestSchema(t *testing.T, ctx context.Context, st *Store) {
	t.Helper()
	for _, table := range postgresTestTables {
		if _, err := st.execContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop table %s: %v", table, err)
		}
	}
	ddl := []string{
		"CREATE TABLE inbounds (id BIGINT PRIMARY KEY)",
		"CREATE TABLE clients (id BIGINT PRIMARY KEY, email TEXT UNIQUE NOT NULL)",
		"CREATE TABLE client_inbounds (client_id BIGINT NOT NULL, inbound_id BIGINT NOT NULL, PRIMARY KEY(client_id, inbound_id))",
		`CREATE TABLE client_traffics (
			id BIGINT PRIMARY KEY,
			inbound_id BIGINT,
			enable BOOLEAN,
			email TEXT UNIQUE NOT NULL,
			up BIGINT,
			down BIGINT,
			expiry_time BIGINT,
			total BIGINT,
			reset BIGINT,
			last_online BIGINT
		)`,
	}
	for _, statement := range ddl {
		if _, err := st.execContext(ctx, statement); err != nil {
			t.Fatalf("create x-ui schema: %v", err)
		}
	}
}

func insertPostgresTraffic(t *testing.T, ctx context.Context, st *Store, id, inboundID int64, enable bool, email string, up, down, total int64) {
	t.Helper()
	if _, err := st.execContext(ctx, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, expiry_time, total, reset, last_online)
		VALUES(?, ?, ?, ?, ?, ?, 0, ?, 0, 0)
	`, id, inboundID, enable, email, up, down, total); err != nil {
		t.Fatalf("insert client traffic: %v", err)
	}
	if _, err := st.execContext(ctx, `INSERT INTO clients(id, email) VALUES(?, ?)`, id, email); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	if _, err := st.execContext(ctx, `INSERT INTO client_inbounds(client_id, inbound_id) VALUES(?, ?)`, id, inboundID); err != nil {
		t.Fatalf("insert client inbound link: %v", err)
	}
}

func TestPostgresValidateMigrateIdempotent(t *testing.T) {
	st, ctx := openPostgresTestStore(t)

	if err := st.ValidateRequiredSchema(ctx); err != nil {
		t.Fatalf("validate schema: %v", err)
	}

	// A decoy table in another schema must not leak into validation.
	if _, err := st.execContext(ctx, `CREATE SCHEMA IF NOT EXISTS xui_factor_decoy`); err != nil {
		t.Fatalf("create decoy schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.execContext(context.Background(), `DROP SCHEMA IF EXISTS xui_factor_decoy CASCADE`)
	})
	if _, err := st.execContext(ctx, `
		CREATE TABLE xui_factor_decoy.client_traffics (id BIGINT, all_time BIGINT)
	`); err != nil {
		t.Fatalf("create decoy table: %v", err)
	}
	if err := st.ValidateRequiredSchema(ctx); err != nil {
		t.Fatalf("decoy schema leaked into validation: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("migrate %d: %v", i+1, err)
		}
	}
	ready, err := st.MetadataReady(ctx)
	if err != nil {
		t.Fatalf("metadata ready: %v", err)
	}
	if !ready {
		t.Fatal("metadata tables missing after migration")
	}
	var version string
	err = st.WithReadTx(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM xui_factor_meta WHERE key = 'schema_version'`).Scan(&version)
	})
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != "3" {
		t.Fatalf("schema version = %q, want 3", version)
	}
}

func TestPostgresClientSelectionAndCounters(t *testing.T) {
	st, ctx := openPostgresTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The client is a member of inbound 10 while the legacy pointer says 99.
	insertPostgresTraffic(t, ctx, st, 1, 10, true, "user@example.com", 100, 200, 0)
	insertPostgresTraffic(t, ctx, st, 2, 11, false, "disabled@example.com", 0, 0, 0)
	if _, err := st.execContext(ctx, `UPDATE client_traffics SET inbound_id = 99 WHERE id = 1`); err != nil {
		t.Fatalf("drift pointer: %v", err)
	}

	inboundID := int64(10)
	var client ClientTraffic
	err := st.WithReadTx(ctx, func(tx *Tx) error {
		var err error
		client, err = tx.FindClient(ctx, "user@example.com", &inboundID)
		return err
	})
	if err != nil {
		t.Fatalf("find client via membership: %v", err)
	}
	if client.ID != 1 || client.Enable != 1 {
		t.Fatalf("client = %#v, want id 1 enabled", client)
	}
	err = st.WithReadTx(ctx, func(tx *Tx) error {
		_, err := tx.FindClient(ctx, "user@example.com", nil)
		return err
	})
	if err != nil {
		t.Fatalf("find client by email: %v", err)
	}

	var candidates []ClientTraffic
	err = st.WithReadTx(ctx, func(tx *Tx) error {
		var err error
		candidates, err = tx.ListClientCandidates(ctx, ClientFilter{InboundID: &inboundID})
		return err
	})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Email != "user@example.com" {
		t.Fatalf("candidates = %#v, want only the enabled member", candidates)
	}

	err = st.WithImmediateTx(ctx, func(tx *Tx) error {
		current, err := tx.ClientTrafficByID(ctx, 1)
		if err != nil {
			return err
		}
		ok, err := tx.UpdateClientCountersCAS(ctx, current, current.Up+10, current.Down+20)
		if err != nil {
			return err
		}
		if !ok {
			return ErrRace
		}
		return nil
	})
	if err != nil {
		t.Fatalf("counter update: %v", err)
	}
	var up, down int64
	if err := st.queryRowContext(ctx, `SELECT up, down FROM client_traffics WHERE id = 1`).Scan(&up, &down); err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if up != 110 || down != 220 {
		t.Fatalf("counters = up %d down %d, want 110/220", up, down)
	}

	// The panel may still carry an integer enable column before its own
	// normalization runs; reads must stay portable across both shapes.
	if _, err := st.execContext(ctx, `
		ALTER TABLE client_traffics ALTER COLUMN enable TYPE integer USING CASE WHEN enable THEN 1 ELSE 0 END
	`); err != nil {
		t.Fatalf("convert enable to integer: %v", err)
	}
	err = st.WithReadTx(ctx, func(tx *Tx) error {
		var err error
		client, err = tx.FindClient(ctx, "user@example.com", &inboundID)
		return err
	})
	if err != nil {
		t.Fatalf("find client with integer enable: %v", err)
	}
	if client.Enable != 1 {
		t.Fatalf("integer enable client = %#v, want enabled", client)
	}
	err = st.WithReadTx(ctx, func(tx *Tx) error {
		var err error
		candidates, err = tx.ListClientCandidates(ctx, ClientFilter{InboundID: &inboundID})
		return err
	})
	if err != nil {
		t.Fatalf("list candidates with integer enable: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Email != "user@example.com" {
		t.Fatalf("integer enable candidates = %#v, want only the enabled member", candidates)
	}
}

func testPtr(value int64) *int64 {
	return &value
}

func TestPostgresRuleLifecycleAndRelink(t *testing.T) {
	st, ctx := openPostgresTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	insertPostgresTraffic(t, ctx, st, 1, 10, true, "user@example.com", 100, 200, 0)

	var ruleID int64
	err := st.WithImmediateTx(ctx, func(tx *Tx) error {
		var err error
		ruleID, err = tx.CreateRule(ctx, Rule{
			InboundID:   10,
			Email:       "user@example.com",
			FactorPPM:   2_000_000,
			State:       StateActive,
			CreatedAt:   100,
			UpdatedAt:   100,
			ActivatedAt: testPtr(100),
		})
		if err != nil {
			return err
		}
		return tx.CreateRuleClient(ctx, RuleClient{
			RuleID:    ruleID,
			TrafficID: 1,
			InboundID: 10,
			Email:     "user@example.com",
			LastUp:    100,
			LastDown:  200,
			UpdatedAt: 100,
		})
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if ruleID <= 0 {
		t.Fatalf("rule id = %d, want identity value", ruleID)
	}

	// The panel replaced the email-keyed traffic row; relink follows the email.
	if _, err := st.execContext(ctx, `DELETE FROM client_traffics WHERE id = 1`); err != nil {
		t.Fatalf("delete traffic row: %v", err)
	}
	if _, err := st.execContext(ctx, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, expiry_time, total, reset, last_online)
		VALUES(2, 10, true, 'user@example.com', 500, 600, 0, 0, 0, 0)
	`); err != nil {
		t.Fatalf("insert replacement row: %v", err)
	}

	var relinked RuleClient
	err = st.WithImmediateTx(ctx, func(tx *Tx) error {
		var err error
		relinked, err = tx.RelinkRuleClientByEmail(ctx, ruleID, 1, 200)
		return err
	})
	if err != nil {
		t.Fatalf("relink: %v", err)
	}
	if relinked.TrafficID != 2 || relinked.LastUp != 500 || relinked.LastDown != 600 || relinked.MissingAt != nil {
		t.Fatalf("relinked = %#v, want traffic 2 baseline 500/600 without missing marker", relinked)
	}

	var active []ActiveRuleClient
	err = st.WithImmediateTx(ctx, func(tx *Tx) error {
		var err error
		active, err = tx.ListActiveRuleClients(ctx)
		return err
	})
	if err != nil {
		t.Fatalf("list active rule clients: %v", err)
	}
	if len(active) != 1 || active[0].Client.TrafficID != 2 {
		t.Fatalf("active = %#v, want one relinked target", active)
	}

	rules, err := st.ListRules(ctx, false)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != 1 || rules[0].ClientCount != 1 || rules[0].EffectiveClientCount != 1 {
		t.Fatalf("rules = %#v, want one effective rule for the relinked client", rules)
	}

	inboundID := int64(10)
	count, err := st.CountActiveRuleClients(ctx, &inboundID)
	if err != nil {
		t.Fatalf("count active rule clients: %v", err)
	}
	if count != 1 {
		t.Fatalf("active rule client count = %d, want 1", count)
	}

	if _, err := st.execContext(ctx, `INSERT INTO xui_factor_overrides(traffic_id, inbound_id, email, factor_ppm, state, created_at, updated_at, activated_at)
		VALUES(2, 10, 'user@example.com', 1500000, 'active', 300, 300, 300)`); err != nil {
		t.Fatalf("insert override: %v", err)
	}
	effective, err := st.CountEffectiveOverrides(ctx)
	if err != nil {
		t.Fatalf("count effective overrides: %v", err)
	}
	if effective != 1 {
		t.Fatalf("effective overrides = %d, want 1", effective)
	}

	events, err := st.ListEvents(ctx, EventFilter{Limit: 5})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestPostgresPolicyUpsertAndListFilter(t *testing.T) {
	st, ctx := openPostgresTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	insertPostgresTraffic(t, ctx, st, 1, 10, true, "user@example.com", 100, 200, 0)

	var first ExcludePolicy
	err := st.WithImmediateTx(ctx, func(tx *Tx) error {
		var err error
		first, err = tx.UpsertExclude(ctx, ExcludePolicy{
			TrafficID: 1, InboundID: 10, Email: "user@example.com",
			State: StateActive, CreatedAt: 100, UpdatedAt: 100, ActivatedAt: testPtr(100),
		})
		return err
	})
	if err != nil {
		t.Fatalf("first exclude: %v", err)
	}

	// Pointer and membership move to inbound 99; the exclude must not duplicate.
	if _, err := st.execContext(ctx, `UPDATE client_traffics SET inbound_id = 99 WHERE id = 1`); err != nil {
		t.Fatalf("drift pointer: %v", err)
	}
	if _, err := st.execContext(ctx, `DELETE FROM client_inbounds WHERE client_id = 1`); err != nil {
		t.Fatalf("clear membership: %v", err)
	}
	if _, err := st.execContext(ctx, `INSERT INTO client_inbounds(client_id, inbound_id) VALUES(1, 99)`); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	var second ExcludePolicy
	err = st.WithImmediateTx(ctx, func(tx *Tx) error {
		var err error
		second, err = tx.UpsertExclude(ctx, ExcludePolicy{
			TrafficID: 1, InboundID: 99, Email: "user@example.com",
			State: StateActive, CreatedAt: 100, UpdatedAt: 200, ActivatedAt: testPtr(200),
		})
		return err
	})
	if err != nil {
		t.Fatalf("second exclude: %v", err)
	}
	if first.ID != second.ID || second.InboundID != 99 {
		t.Fatalf("exclude rows duplicated or stale: first=%#v second=%#v", first, second)
	}

	driftedInbound := int64(99)
	excludes, err := st.ListExcludes(ctx, false, &driftedInbound)
	if err != nil {
		t.Fatalf("list excludes: %v", err)
	}
	if len(excludes) != 1 {
		t.Fatalf("excludes = %#v, want one membership match", excludes)
	}

	meta := int64(0)
	err = st.WithImmediateTx(ctx, func(tx *Tx) error {
		if err := tx.SetMetaInt64(ctx, "last_auto_cleanup_at", 1_700_000_000); err != nil {
			return err
		}
		var ok bool
		var err error
		meta, ok, err = tx.GetMetaInt64(ctx, "last_auto_cleanup_at")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatal("meta value missing after set")
		}
		return nil
	})
	if err != nil || meta != 1_700_000_000 {
		t.Fatalf("meta roundtrip = %d (%v), want 1700000000", meta, err)
	}

	// The panel replaced the traffic row; re-issuing the exclude must move the
	// stored row instead of inserting a duplicate.
	if _, err := st.execContext(ctx, `DELETE FROM client_traffics WHERE id = 1`); err != nil {
		t.Fatalf("delete replaced row: %v", err)
	}
	if _, err := st.execContext(ctx, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, expiry_time, total, reset, last_online)
		VALUES(2, 99, true, 'user@example.com', 0, 0, 0, 0, 0, 0)
	`); err != nil {
		t.Fatalf("insert replacement row: %v", err)
	}

	var moved ExcludePolicy
	err = st.WithImmediateTx(ctx, func(tx *Tx) error {
		var err error
		moved, err = tx.UpsertExclude(ctx, ExcludePolicy{
			TrafficID: 2, InboundID: 99, Email: "user@example.com",
			State: StateActive, CreatedAt: 100, UpdatedAt: 300, ActivatedAt: testPtr(300),
		})
		return err
	})
	if err != nil {
		t.Fatalf("reissue exclude: %v", err)
	}
	if moved.ID != first.ID || moved.TrafficID != 2 {
		t.Fatalf("reissued exclude = %#v, want row %d moved to traffic 2", moved, first.ID)
	}

	var active ExcludePolicy
	err = st.WithReadTx(ctx, func(tx *Tx) error {
		var err error
		active, err = tx.FindActiveExcludeByEmail(ctx, "user@example.com")
		return err
	})
	if err != nil {
		t.Fatalf("find active exclude by email: %v", err)
	}
	if active.ID != first.ID || active.TrafficID != 2 {
		t.Fatalf("active exclude = %#v, want the moved row", active)
	}
}

func TestPostgresWriteProbe(t *testing.T) {
	st, ctx := openPostgresTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.ProbeWrite(ctx); err != nil {
		t.Fatalf("probe with metadata: %v", err)
	}

	// Without metadata the probe falls back to creating its table inside the
	// rolled-back transaction and must leave nothing behind.
	if _, err := st.execContext(ctx, `DROP TABLE xui_factor_meta`); err != nil {
		t.Fatalf("drop metadata table: %v", err)
	}
	if err := st.ProbeWrite(ctx); err != nil {
		t.Fatalf("probe without metadata: %v", err)
	}
	var leftover string
	if err := st.queryRowContext(ctx, `SELECT COALESCE(to_regclass('xui_factor_write_probe')::text, '')`).Scan(&leftover); err != nil {
		t.Fatalf("check probe table: %v", err)
	}
	if leftover != "" {
		t.Fatalf("probe table left behind: %s", leftover)
	}
}

func TestPostgresBackupWithPgDump(t *testing.T) {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump is not installed")
	}
	st, ctx := openPostgresTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	backupDir := t.TempDir()
	path, err := st.Backup(ctx, backupDir, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !strings.HasSuffix(path, ".dump") {
		t.Fatalf("backup path = %s, want .dump suffix", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("backup %s is empty", path)
	}
}
