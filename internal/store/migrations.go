package store

var migrationTableStatements = []string{
	`CREATE TABLE IF NOT EXISTS xui_factor_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_rules (
		__ID_COLUMN__,
		name TEXT,
		inbound_id BIGINT,
		email TEXT NOT NULL,
		factor_ppm BIGINT NOT NULL,
		state TEXT NOT NULL,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		activated_at BIGINT,
		paused_at BIGINT,
		disabled_at BIGINT
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_rule_clients (
		rule_id BIGINT NOT NULL,
		traffic_id BIGINT NOT NULL,
		inbound_id BIGINT NOT NULL,
		email TEXT NOT NULL,
		last_up BIGINT NOT NULL,
		last_down BIGINT NOT NULL,
		rem_up BIGINT NOT NULL DEFAULT 0,
		rem_down BIGINT NOT NULL DEFAULT 0,
		missing_since BIGINT,
		updated_at BIGINT NOT NULL,
		PRIMARY KEY(rule_id, traffic_id)
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_scopes (
		rule_id BIGINT PRIMARY KEY,
		inbound_id BIGINT,
		limited_only BIGINT NOT NULL DEFAULT 0,
		include_disabled_clients BIGINT NOT NULL DEFAULT 0,
		once BIGINT NOT NULL DEFAULT 0,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_excludes (
		__ID_COLUMN__,
		traffic_id BIGINT NOT NULL,
		inbound_id BIGINT NOT NULL,
		email TEXT NOT NULL,
		state TEXT NOT NULL,
		note TEXT,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		activated_at BIGINT,
		disabled_at BIGINT,
		UNIQUE(traffic_id, inbound_id, email)
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_overrides (
		__ID_COLUMN__,
		traffic_id BIGINT NOT NULL,
		inbound_id BIGINT NOT NULL,
		email TEXT NOT NULL,
		factor_ppm BIGINT NOT NULL,
		state TEXT NOT NULL,
		note TEXT,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		activated_at BIGINT,
		disabled_at BIGINT,
		UNIQUE(traffic_id, inbound_id, email)
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_events (
		__ID_COLUMN__,
		rule_id BIGINT,
		event_type TEXT NOT NULL,
		message TEXT,
		created_at BIGINT NOT NULL
	)`,
}

var migrationIndexStatements = []string{
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_rules_state
		ON xui_factor_rules(state)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_rules_target_state
		ON xui_factor_rules(email, inbound_id, state)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_rule_clients_traffic
		ON xui_factor_rule_clients(traffic_id)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_rule_clients_missing
		ON xui_factor_rule_clients(missing_since)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_scopes_inbound
		ON xui_factor_scopes(inbound_id)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_excludes_state
		ON xui_factor_excludes(state)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_xui_factor_excludes_identity
		ON xui_factor_excludes(traffic_id, inbound_id, email)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_excludes_target_state
		ON xui_factor_excludes(traffic_id, inbound_id, email, state)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_excludes_inbound_state
		ON xui_factor_excludes(inbound_id, state)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_overrides_state
		ON xui_factor_overrides(state)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_xui_factor_overrides_identity
		ON xui_factor_overrides(traffic_id, inbound_id, email)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_overrides_target_state
		ON xui_factor_overrides(traffic_id, inbound_id, email, state)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_overrides_inbound_state
		ON xui_factor_overrides(inbound_id, state)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_events_created
		ON xui_factor_events(created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_events_type_created
		ON xui_factor_events(event_type, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_events_rule_created
		ON xui_factor_events(rule_id, created_at)`,
}

var migrationColumns = map[string][]migrationColumn{
	"xui_factor_meta": {
		{name: "key", ddl: "key TEXT"},
		{name: "value", ddl: "value TEXT NOT NULL DEFAULT ''"},
		{name: "updated_at", ddl: "updated_at BIGINT NOT NULL DEFAULT 0"},
	},
	"xui_factor_rules": {
		{name: "name", ddl: "name TEXT"},
		{name: "inbound_id", ddl: "inbound_id BIGINT"},
		{name: "email", ddl: "email TEXT NOT NULL DEFAULT ''"},
		{name: "factor_ppm", ddl: "factor_ppm BIGINT NOT NULL DEFAULT 1000000"},
		{name: "state", ddl: "state TEXT NOT NULL DEFAULT 'active'"},
		{name: "created_at", ddl: "created_at BIGINT NOT NULL DEFAULT 0"},
		{name: "updated_at", ddl: "updated_at BIGINT NOT NULL DEFAULT 0"},
		{name: "activated_at", ddl: "activated_at BIGINT"},
		{name: "paused_at", ddl: "paused_at BIGINT"},
		{name: "disabled_at", ddl: "disabled_at BIGINT"},
	},
	"xui_factor_rule_clients": {
		{name: "rule_id", ddl: "rule_id BIGINT NOT NULL DEFAULT 0"},
		{name: "traffic_id", ddl: "traffic_id BIGINT NOT NULL DEFAULT 0"},
		{name: "inbound_id", ddl: "inbound_id BIGINT NOT NULL DEFAULT 0"},
		{name: "email", ddl: "email TEXT NOT NULL DEFAULT ''"},
		{name: "last_up", ddl: "last_up BIGINT NOT NULL DEFAULT 0"},
		{name: "last_down", ddl: "last_down BIGINT NOT NULL DEFAULT 0"},
		{name: "rem_up", ddl: "rem_up BIGINT NOT NULL DEFAULT 0"},
		{name: "rem_down", ddl: "rem_down BIGINT NOT NULL DEFAULT 0"},
		{name: "missing_since", ddl: "missing_since BIGINT"},
		{name: "updated_at", ddl: "updated_at BIGINT NOT NULL DEFAULT 0"},
	},
	"xui_factor_scopes": {
		{name: "rule_id", ddl: "rule_id BIGINT NOT NULL DEFAULT 0"},
		{name: "inbound_id", ddl: "inbound_id BIGINT"},
		{name: "limited_only", ddl: "limited_only BIGINT NOT NULL DEFAULT 0"},
		{name: "include_disabled_clients", ddl: "include_disabled_clients BIGINT NOT NULL DEFAULT 0"},
		{name: "once", ddl: "once BIGINT NOT NULL DEFAULT 0"},
		{name: "created_at", ddl: "created_at BIGINT NOT NULL DEFAULT 0"},
		{name: "updated_at", ddl: "updated_at BIGINT NOT NULL DEFAULT 0"},
	},
	"xui_factor_excludes": {
		{name: "traffic_id", ddl: "traffic_id BIGINT NOT NULL DEFAULT 0"},
		{name: "inbound_id", ddl: "inbound_id BIGINT NOT NULL DEFAULT 0"},
		{name: "email", ddl: "email TEXT NOT NULL DEFAULT ''"},
		{name: "state", ddl: "state TEXT NOT NULL DEFAULT 'active'"},
		{name: "note", ddl: "note TEXT"},
		{name: "created_at", ddl: "created_at BIGINT NOT NULL DEFAULT 0"},
		{name: "updated_at", ddl: "updated_at BIGINT NOT NULL DEFAULT 0"},
		{name: "activated_at", ddl: "activated_at BIGINT"},
		{name: "disabled_at", ddl: "disabled_at BIGINT"},
	},
	"xui_factor_overrides": {
		{name: "traffic_id", ddl: "traffic_id BIGINT NOT NULL DEFAULT 0"},
		{name: "inbound_id", ddl: "inbound_id BIGINT NOT NULL DEFAULT 0"},
		{name: "email", ddl: "email TEXT NOT NULL DEFAULT ''"},
		{name: "factor_ppm", ddl: "factor_ppm BIGINT NOT NULL DEFAULT 1000000"},
		{name: "state", ddl: "state TEXT NOT NULL DEFAULT 'active'"},
		{name: "note", ddl: "note TEXT"},
		{name: "created_at", ddl: "created_at BIGINT NOT NULL DEFAULT 0"},
		{name: "updated_at", ddl: "updated_at BIGINT NOT NULL DEFAULT 0"},
		{name: "activated_at", ddl: "activated_at BIGINT"},
		{name: "disabled_at", ddl: "disabled_at BIGINT"},
	},
	"xui_factor_events": {
		{name: "rule_id", ddl: "rule_id BIGINT"},
		{name: "event_type", ddl: "event_type TEXT NOT NULL DEFAULT ''"},
		{name: "message", ddl: "message TEXT"},
		{name: "created_at", ddl: "created_at BIGINT NOT NULL DEFAULT 0"},
	},
}

// migrationPolicyDedupeStatements normalize policy rows written before the
// inbound pointer stopped being part of the client identity. A client could
// accumulate one active exclude or override per inbound pointer; keep the
// latest row per client and disable the rest so policy stays unambiguous.
// Placeholders receive the migration timestamp.
var migrationPolicyDedupeStatements = []string{
	`UPDATE xui_factor_excludes
		SET state = 'disabled', disabled_at = ?, updated_at = ?
		WHERE state = 'active'
			AND id NOT IN (
				SELECT MAX(id)
				FROM xui_factor_excludes
				WHERE state = 'active'
				GROUP BY traffic_id, email
			)`,
	`UPDATE xui_factor_overrides
		SET state = 'disabled', disabled_at = ?, updated_at = ?
		WHERE state = 'active'
			AND id NOT IN (
				SELECT MAX(id)
				FROM xui_factor_overrides
				WHERE state = 'active'
				GROUP BY traffic_id, email
			)`,
}

type migrationColumn struct {
	name string
	ddl  string
}

// migrationDropColumns removes metadata columns from earlier XuiFactor versions
// that are no longer part of the schema. 3x-ui 3.x dropped the all_time counter
// they were derived from, so the stored all-time baselines are dead weight.
var migrationDropColumns = []struct {
	table   string
	columns []string
}{
	{table: "xui_factor_rule_clients", columns: []string{"last_all_time", "rem_all_time"}},
}
