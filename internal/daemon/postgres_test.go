package daemon

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PdYrust/XuiFactor/internal/config"
	"github.com/PdYrust/XuiFactor/internal/engine"
	"github.com/PdYrust/XuiFactor/internal/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Postgres integration test; runs only when TEST_POSTGRES_DSN points at a
// throwaway database. CI provides this database; local runs skip without it.

func TestPostgresTickAppliesFactorThroughMembership(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set; skipping postgres integration test")
	}
	ctx := context.Background()
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open raw postgres connection: %v", err)
	}
	defer raw.Close()
	// Serialize against other test packages resetting the same database.
	lockConn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve postgres lock connection: %v", err)
	}
	lockKey := int64(0x58554946_54455354) // XUIFTEST
	if _, err := lockConn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		t.Fatalf("acquire postgres test lock: %v", err)
	}
	defer func() {
		_, _ = lockConn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey)
		lockConn.Close()
	}()

	st, err := store.OpenPostgres(ctx, dsn, 5*time.Second)
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	defer st.Close()

	for _, table := range []string{
		"client_traffics", "client_inbounds", "clients", "inbounds",
		"xui_factor_rule_clients", "xui_factor_rules", "xui_factor_scopes",
		"xui_factor_excludes", "xui_factor_overrides", "xui_factor_events", "xui_factor_meta",
	} {
		if _, err := raw.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop table %s: %v", table, err)
		}
	}
	for _, statement := range []string{
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
		// The client is a member of inbound 10 while the legacy pointer says 99.
		`INSERT INTO clients(id, email) VALUES(1, 'user@example.com')`,
		`INSERT INTO client_inbounds(client_id, inbound_id) VALUES(1, 10)`,
		`INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, expiry_time, total, reset, last_online)
		 VALUES(1, 99, true, 'user@example.com', 100, 200, 0, 0, 0, 0)`,
	} {
		if _, err := raw.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed postgres schema: %v", err)
		}
	}
	if err := st.EnsureReady(ctx); err != nil {
		t.Fatalf("ensure ready: %v", err)
	}

	svc := engine.NewWithClock(st, func() time.Time { return time.Unix(1_700_000_000, 0) })
	inboundID := int64(10)
	if _, err := svc.Enable(ctx, engine.EnableRequest{Email: "user@example.com", InboundID: &inboundID, Factor: "2"}); err != nil {
		t.Fatalf("enable through membership: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE client_traffics SET up = 110, down = 220 WHERE id = 1`); err != nil {
		t.Fatalf("advance counters: %v", err)
	}

	cfg := config.Defaults()
	cfg.BusyTimeout = 5 * time.Second
	cfg.AutoCleanup = false
	runner := NewWithClock(st, cfg, nil, nil, func() time.Time { return time.Unix(1_700_000_000, 0) })
	result, err := runner.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if result.Applied != 1 {
		t.Fatalf("result = %#v, want one applied factor", result)
	}

	var up, down int64
	if err := raw.QueryRowContext(ctx, `SELECT up, down FROM client_traffics WHERE id = 1`).Scan(&up, &down); err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if up != 120 || down != 240 {
		t.Fatalf("counters = up %d down %d, want 120/240", up, down)
	}
}
