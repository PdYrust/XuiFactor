package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A master pushes client usage to its nodes every 30 seconds. The panel treats
// rows older than this window as coming from a master that stopped managing it.
const managedPanelWindow = 24 * time.Hour

// PanelRole describes where this panel sits in a 3x-ui master/node setup.
type PanelRole struct {
	Nodes   int
	Managed bool
}

func (r PanelRole) Name() string {
	switch {
	case r.Managed:
		return "node"
	case r.Nodes > 0:
		return "master"
	default:
		return "standalone"
	}
}

func (s *Store) PanelRole(ctx context.Context, now time.Time) (PanelRole, error) {
	return panelRoleWith(ctx, s.tableExists, s.queryRowContext, s.driver, now)
}

func (tx *Tx) PanelRole(ctx context.Context, now time.Time) (PanelRole, error) {
	return panelRoleWith(ctx, tx.TableExists, tx.QueryRowContext, tx.driver, now)
}

func panelRoleWith(ctx context.Context, tableExists func(context.Context, string) (bool, error), queryRow queryRowFunc, driver string, now time.Time) (PanelRole, error) {
	var role PanelRole
	exists, err := tableExists(ctx, "nodes")
	if err != nil {
		return PanelRole{}, err
	}
	if exists {
		if err := queryRow(ctx, "SELECT COUNT(*) FROM nodes WHERE "+enableTrueExpr(driver, "")).Scan(&role.Nodes); err != nil {
			return PanelRole{}, wrapDatabaseError("read panel nodes", err)
		}
	}
	exists, err = tableExists(ctx, "client_global_traffics")
	if err != nil {
		return PanelRole{}, err
	}
	if exists {
		var found int
		err := queryRow(ctx, `
			SELECT 1
			FROM client_global_traffics
			WHERE updated_at >= ?
			LIMIT 1
		`, now.Add(-managedPanelWindow).UnixMilli()).Scan(&found)
		switch {
		case err == nil:
			role.Managed = true
		case !errors.Is(err, sql.ErrNoRows):
			return PanelRole{}, wrapDatabaseError("read master usage", err)
		}
	}
	return role, nil
}
