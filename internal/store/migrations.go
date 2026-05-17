package store

var migrationStatements = []string{
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
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_excludes_target_state
		ON xui_factor_excludes(traffic_id, inbound_id, email, state)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_excludes_inbound_state
		ON xui_factor_excludes(inbound_id, state)`,
	`CREATE INDEX IF NOT EXISTS idx_xui_factor_overrides_state
		ON xui_factor_overrides(state)`,
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
