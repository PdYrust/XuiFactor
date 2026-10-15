package store

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func postgresEnvMap(env []string) map[string]string {
	lookup := make(map[string]string, len(env))
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		lookup[name] = value
	}
	return lookup
}

func TestPostgresCommandEnvForwardsTLSAndStripsPGVars(t *testing.T) {
	// Parse a DSN pgconn can read without touching the filesystem, then hand
	// the TLS-bearing DSN to the environment builder: it only parses the
	// connection string for sslmode and sslrootcert.
	parsed, err := pgconn.ParseConfig("postgres://user:secret@db.example.com:5432/xui?sslmode=require")
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	dsn := "postgres://user:secret@db.example.com:5432/xui?sslmode=verify-full&sslrootcert=/etc/ca.pem"
	env := postgresEnvMap(postgresCommandEnv([]string{
		"PATH=/usr/bin",
		"HOME=/root",
		"PGSSLMODE=prefer",
		"PGOPTIONS=-c statement_timeout=1",
		"PGSERVICE=prod",
	}, parsed, dsn))

	for _, stripped := range []string{"PGOPTIONS", "PGSERVICE"} {
		if _, ok := env[stripped]; ok {
			t.Fatalf("%s was not stripped from the dump environment", stripped)
		}
	}
	if env["PGSSLMODE"] != "verify-full" {
		t.Fatalf("PGSSLMODE = %q, want verify-full", env["PGSSLMODE"])
	}
	if env["PGSSLROOTCERT"] != "/etc/ca.pem" {
		t.Fatalf("PGSSLROOTCERT = %q, want /etc/ca.pem", env["PGSSLROOTCERT"])
	}
	if env["PGPASSWORD"] != "secret" || env["PGHOST"] != "db.example.com" || env["PGUSER"] != "user" || env["PGDATABASE"] != "xui" {
		t.Fatalf("connection env wrong: %#v", env)
	}
	if env["PATH"] != "/usr/bin" || env["HOME"] != "/root" {
		t.Fatalf("unrelated environment was lost: %#v", env)
	}
}

func TestPostgresCommandEnvTLSModes(t *testing.T) {
	disabled := "postgres://user@h:5432/db?sslmode=disable"
	parsedDisabled, err := pgconn.ParseConfig(disabled)
	if err != nil {
		t.Fatalf("parse disabled dsn: %v", err)
	}
	if got := postgresEnvMap(postgresCommandEnv(nil, parsedDisabled, disabled))["PGSSLMODE"]; got != "disable" {
		t.Fatalf("disable dsn PGSSLMODE = %q, want disable", got)
	}

	// Without an explicit sslmode, libpq's default already matches the pgx
	// default, so no value is forced.
	plain := "postgres://user@h:5432/db"
	parsedPlain, err := pgconn.ParseConfig(plain)
	if err != nil {
		t.Fatalf("parse plain dsn: %v", err)
	}
	if _, ok := postgresEnvMap(postgresCommandEnv(nil, parsedPlain, plain))["PGSSLMODE"]; ok {
		t.Fatalf("plain dsn should not force PGSSLMODE")
	}

	keyword := "host=h port=5432 user=u dbname=d sslmode=require"
	parsedKeyword, err := pgconn.ParseConfig(keyword)
	if err != nil {
		t.Fatalf("parse keyword dsn: %v", err)
	}
	if got := postgresEnvMap(postgresCommandEnv(nil, parsedKeyword, keyword))["PGSSLMODE"]; got != "require" {
		t.Fatalf("keyword dsn PGSSLMODE = %q, want require", got)
	}
}
