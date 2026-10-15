package store

import (
	"context"
	"database/sql"
	"errors"
)

// clientInboundMembership matches a client by inbound attachment through the
// authoritative 3x-ui 3.x link (clients + client_inbounds). client_traffics
// carries one shared row per email and its inbound_id is a stale-prone legacy
// pointer, so membership never reads that column.
func clientInboundMembership(emailExpr string) string {
	return `EXISTS (
			SELECT 1 FROM clients c
			INNER JOIN client_inbounds ci ON ci.client_id = c.id
			WHERE ci.inbound_id = ? AND c.email = ` + emailExpr + `
		)`
}

func (tx *Tx) ClientIsInboundMember(ctx context.Context, email string, inboundID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM clients c
		INNER JOIN client_inbounds ci ON ci.client_id = c.id
		WHERE c.email = ? AND ci.inbound_id = ?
		LIMIT 1
	`, email, inboundID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// AdoptRuleClientInbound records the live inbound pointer on a rule client.
// On 3x-ui 3.x a client can be attached to several inbounds and the pointer
// moves between them, so drift is informational rather than an identity change.
func (tx *Tx) AdoptRuleClientInbound(ctx context.Context, ruleID, trafficID, inboundID, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rule_clients
		SET inbound_id = ?, updated_at = ?
		WHERE rule_id = ? AND traffic_id = ?
	`, inboundID, now, ruleID, trafficID)
	return err
}

func (tx *Tx) ClientTrafficByEmail(ctx context.Context, email string) (ClientTraffic, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, inbound_id, `+enableAsIntExpr(tx.driver)+`, email, up, down, total
		FROM client_traffics
		WHERE email = ?
		ORDER BY id
		LIMIT 1
	`, email)
	var c ClientTraffic
	if err := row.Scan(&c.ID, &c.InboundID, &c.Enable, &c.Email, &c.Up, &c.Down, &c.Total); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ClientTraffic{}, ErrNotFound
		}
		return ClientTraffic{}, err
	}
	return c, nil
}

// RelinkRuleClientByEmail re-points a rule client at the current traffic row
// for its email. 3x-ui 3.x keeps one shared row per email: an upgrade or a
// client rename replaces the row, so a rule whose stored traffic id vanished
// is dead until it follows the email.
func (tx *Tx) RelinkRuleClientByEmail(ctx context.Context, ruleID, trafficID, now int64) (RuleClient, error) {
	rc, err := tx.RuleClient(ctx, ruleID, trafficID)
	if err != nil {
		return RuleClient{}, err
	}
	current, err := tx.ClientTrafficByEmail(ctx, rc.Email)
	if err != nil {
		return RuleClient{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rule_clients
		SET traffic_id = ?, inbound_id = ?, last_up = ?, last_down = ?,
			rem_up = 0, rem_down = 0, missing_since = NULL, updated_at = ?
		WHERE rule_id = ? AND traffic_id = ?
			AND NOT EXISTS (
				SELECT 1 FROM xui_factor_rule_clients other
				WHERE other.rule_id = xui_factor_rule_clients.rule_id AND other.traffic_id = ?
			)
	`, current.ID, current.InboundID, current.Up, current.Down, now, ruleID, trafficID, current.ID)
	if err != nil {
		return RuleClient{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RuleClient{}, err
	}
	if affected != 1 {
		return RuleClient{}, ErrConflict
	}
	return tx.RuleClient(ctx, ruleID, current.ID)
}
