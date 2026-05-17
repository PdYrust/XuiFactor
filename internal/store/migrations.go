package store

var migrationTableStatements = []string{
	`CREATE TABLE IF NOT EXISTS xui_factor_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		inbound_id INTEGER,
		email TEXT NOT NULL,
		factor_ppm INTEGER NOT NULL,
		state TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		activated_at INTEGER,
		paused_at INTEGER,
		disabled_at INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_rule_clients (
		rule_id INTEGER NOT NULL,
		traffic_id INTEGER NOT NULL,
		inbound_id INTEGER NOT NULL,
		email TEXT NOT NULL,
		last_up INTEGER NOT NULL,
		last_down INTEGER NOT NULL,
		last_all_time INTEGER NOT NULL,
		rem_up INTEGER NOT NULL DEFAULT 0,
		rem_down INTEGER NOT NULL DEFAULT 0,
		rem_all_time INTEGER NOT NULL DEFAULT 0,
		missing_since INTEGER,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(rule_id, traffic_id)
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_scopes (
		rule_id INTEGER PRIMARY KEY,
		inbound_id INTEGER,
		limited_only INTEGER NOT NULL DEFAULT 0,
		include_disabled_clients INTEGER NOT NULL DEFAULT 0,
		once INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_excludes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		traffic_id INTEGER NOT NULL,
		inbound_id INTEGER NOT NULL,
		email TEXT NOT NULL,
		state TEXT NOT NULL,
		note TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		activated_at INTEGER,
		disabled_at INTEGER,
		UNIQUE(traffic_id, inbound_id, email)
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_overrides (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		traffic_id INTEGER NOT NULL,
		inbound_id INTEGER NOT NULL,
		email TEXT NOT NULL,
		factor_ppm INTEGER NOT NULL,
		state TEXT NOT NULL,
		note TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		activated_at INTEGER,
		disabled_at INTEGER,
		UNIQUE(traffic_id, inbound_id, email)
	)`,
	`CREATE TABLE IF NOT EXISTS xui_factor_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		rule_id INTEGER,
		event_type TEXT NOT NULL,
		message TEXT,
		created_at INTEGER NOT NULL
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
		{name: "updated_at", ddl: "updated_at INTEGER NOT NULL DEFAULT 0"},
	},
	"xui_factor_rules": {
		{name: "name", ddl: "name TEXT"},
		{name: "inbound_id", ddl: "inbound_id INTEGER"},
		{name: "email", ddl: "email TEXT NOT NULL DEFAULT ''"},
		{name: "factor_ppm", ddl: "factor_ppm INTEGER NOT NULL DEFAULT 1000000"},
		{name: "state", ddl: "state TEXT NOT NULL DEFAULT 'active'"},
		{name: "created_at", ddl: "created_at INTEGER NOT NULL DEFAULT 0"},
		{name: "updated_at", ddl: "updated_at INTEGER NOT NULL DEFAULT 0"},
		{name: "activated_at", ddl: "activated_at INTEGER"},
		{name: "paused_at", ddl: "paused_at INTEGER"},
		{name: "disabled_at", ddl: "disabled_at INTEGER"},
	},
	"xui_factor_rule_clients": {
		{name: "rule_id", ddl: "rule_id INTEGER NOT NULL DEFAULT 0"},
		{name: "traffic_id", ddl: "traffic_id INTEGER NOT NULL DEFAULT 0"},
		{name: "inbound_id", ddl: "inbound_id INTEGER NOT NULL DEFAULT 0"},
		{name: "email", ddl: "email TEXT NOT NULL DEFAULT ''"},
		{name: "last_up", ddl: "last_up INTEGER NOT NULL DEFAULT 0"},
		{name: "last_down", ddl: "last_down INTEGER NOT NULL DEFAULT 0"},
		{name: "last_all_time", ddl: "last_all_time INTEGER NOT NULL DEFAULT 0"},
		{name: "rem_up", ddl: "rem_up INTEGER NOT NULL DEFAULT 0"},
		{name: "rem_down", ddl: "rem_down INTEGER NOT NULL DEFAULT 0"},
		{name: "rem_all_time", ddl: "rem_all_time INTEGER NOT NULL DEFAULT 0"},
		{name: "missing_since", ddl: "missing_since INTEGER"},
		{name: "updated_at", ddl: "updated_at INTEGER NOT NULL DEFAULT 0"},
	},
	"xui_factor_scopes": {
		{name: "rule_id", ddl: "rule_id INTEGER NOT NULL DEFAULT 0"},
		{name: "inbound_id", ddl: "inbound_id INTEGER"},
		{name: "limited_only", ddl: "limited_only INTEGER NOT NULL DEFAULT 0"},
		{name: "include_disabled_clients", ddl: "include_disabled_clients INTEGER NOT NULL DEFAULT 0"},
		{name: "once", ddl: "once INTEGER NOT NULL DEFAULT 0"},
		{name: "created_at", ddl: "created_at INTEGER NOT NULL DEFAULT 0"},
		{name: "updated_at", ddl: "updated_at INTEGER NOT NULL DEFAULT 0"},
	},
	"xui_factor_excludes": {
		{name: "traffic_id", ddl: "traffic_id INTEGER NOT NULL DEFAULT 0"},
		{name: "inbound_id", ddl: "inbound_id INTEGER NOT NULL DEFAULT 0"},
		{name: "email", ddl: "email TEXT NOT NULL DEFAULT ''"},
		{name: "state", ddl: "state TEXT NOT NULL DEFAULT 'active'"},
		{name: "note", ddl: "note TEXT"},
		{name: "created_at", ddl: "created_at INTEGER NOT NULL DEFAULT 0"},
		{name: "updated_at", ddl: "updated_at INTEGER NOT NULL DEFAULT 0"},
		{name: "activated_at", ddl: "activated_at INTEGER"},
		{name: "disabled_at", ddl: "disabled_at INTEGER"},
	},
	"xui_factor_overrides": {
		{name: "traffic_id", ddl: "traffic_id INTEGER NOT NULL DEFAULT 0"},
		{name: "inbound_id", ddl: "inbound_id INTEGER NOT NULL DEFAULT 0"},
		{name: "email", ddl: "email TEXT NOT NULL DEFAULT ''"},
		{name: "factor_ppm", ddl: "factor_ppm INTEGER NOT NULL DEFAULT 1000000"},
		{name: "state", ddl: "state TEXT NOT NULL DEFAULT 'active'"},
		{name: "note", ddl: "note TEXT"},
		{name: "created_at", ddl: "created_at INTEGER NOT NULL DEFAULT 0"},
		{name: "updated_at", ddl: "updated_at INTEGER NOT NULL DEFAULT 0"},
		{name: "activated_at", ddl: "activated_at INTEGER"},
		{name: "disabled_at", ddl: "disabled_at INTEGER"},
	},
	"xui_factor_events": {
		{name: "rule_id", ddl: "rule_id INTEGER"},
		{name: "event_type", ddl: "event_type TEXT NOT NULL DEFAULT ''"},
		{name: "message", ddl: "message TEXT"},
		{name: "created_at", ddl: "created_at INTEGER NOT NULL DEFAULT 0"},
	},
}

type migrationColumn struct {
	name string
	ddl  string
}
