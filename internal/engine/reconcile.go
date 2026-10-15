package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/PdYrust/XuiFactor/internal/store"
)

type ReconcileRequest struct {
	InboundID *int64
	DryRun    bool
}

type ReconcileResult struct {
	Checked          int
	Reconciled       int
	Orphaned         int
	DisabledClients  int
	Superseded       int
	Conflicts        int
	PoliciesRepaired int
}

func (r ReconcileResult) Summary() string {
	return fmt.Sprintf("checked=%d reconciled=%d orphaned=%d disabled_clients=%d superseded=%d conflicts=%d policies_repaired=%d",
		r.Checked,
		r.Reconciled,
		r.Orphaned,
		r.DisabledClients,
		r.Superseded,
		r.Conflicts,
		r.PoliciesRepaired,
	)
}

func (s *Service) Reconcile(ctx context.Context, req ReconcileRequest) (ReconcileResult, error) {
	now := s.now().Unix()
	var result ReconcileResult
	err := s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		var err error
		result, err = ReconcileTx(ctx, tx, now, req)
		return err
	})
	return result, err
}

func ReconcileTx(ctx context.Context, tx *store.Tx, now int64, req ReconcileRequest) (ReconcileResult, error) {
	if now <= 0 {
		return ReconcileResult{}, errors.New("reconcile now must be positive")
	}
	rules, err := tx.ListRulesInStates(ctx, req.InboundID, store.StateActive)
	if err != nil {
		return ReconcileResult{}, err
	}
	scopes, err := tx.ListActivePersistentScopes(ctx)
	if err != nil {
		return ReconcileResult{}, err
	}

	var result ReconcileResult
	for _, rule := range rules {
		if rule.Scope != nil {
			continue
		}
		result.Checked++
		step, err := reconcileSingleRule(ctx, tx, rule, scopes, now, req.DryRun)
		if err != nil {
			return ReconcileResult{}, err
		}
		result.Reconciled += step.Reconciled
		result.Orphaned += step.Orphaned
		result.DisabledClients += step.DisabledClients
		result.Superseded += step.Superseded
		result.Conflicts += step.Conflicts
	}

	repaired, err := reconcilePolicies(ctx, tx, req, now)
	if err != nil {
		return ReconcileResult{}, err
	}
	result.PoliciesRepaired = repaired
	return result, nil
}

// reconcilePolicies moves active excludes and overrides onto the client's
// current traffic row. 3x-ui 3.x replaces the email-keyed row on upgrade or
// rename, which would otherwise silently drop the policy.
func reconcilePolicies(ctx context.Context, tx *store.Tx, req ReconcileRequest, now int64) (int, error) {
	excludes, err := tx.ListActiveExcludes(ctx)
	if err != nil {
		return 0, err
	}
	overrides, err := tx.ListActiveOverrides(ctx)
	if err != nil {
		return 0, err
	}

	repaired := 0
	for _, exclude := range excludes {
		current, err := resolvePolicyTraffic(ctx, tx, exclude.TrafficID, exclude.Email)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if req.InboundID != nil {
			member, err := tx.ClientIsInboundMember(ctx, exclude.Email, *req.InboundID)
			if err != nil {
				return 0, err
			}
			if !member {
				continue
			}
		}
		if current.ID == exclude.TrafficID && current.InboundID == exclude.InboundID {
			continue
		}
		repaired++
		if req.DryRun {
			continue
		}
		if err := tx.RelinkExcludeRow(ctx, exclude, current, now); err != nil {
			return 0, err
		}
	}

	for _, override := range overrides {
		current, err := resolvePolicyTraffic(ctx, tx, override.TrafficID, override.Email)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if req.InboundID != nil {
			member, err := tx.ClientIsInboundMember(ctx, override.Email, *req.InboundID)
			if err != nil {
				return 0, err
			}
			if !member {
				continue
			}
		}
		if current.ID == override.TrafficID && current.InboundID == override.InboundID {
			continue
		}
		repaired++
		if req.DryRun {
			continue
		}
		if err := tx.RelinkOverrideRow(ctx, override, current, now); err != nil {
			return 0, err
		}
	}
	return repaired, nil
}

func resolvePolicyTraffic(ctx context.Context, tx *store.Tx, trafficID int64, email string) (store.ClientTraffic, error) {
	current, err := tx.ClientTrafficByID(ctx, trafficID)
	if err == nil && current.Email == email {
		return current, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.ClientTraffic{}, err
	}
	return tx.ClientTrafficByEmail(ctx, email)
}

func reconcileSingleRule(ctx context.Context, tx *store.Tx, rule store.Rule, scopes []store.Scope, now int64, dryRun bool) (ReconcileResult, error) {
	clients, err := tx.RuleClientsForRule(ctx, rule.ID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if len(clients) == 0 {
		return orphanRule(ctx, tx, rule, now, dryRun, "active rule has no materialized clients")
	}
	if len(clients) != 1 {
		result, err := orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("active single-user rule has %d materialized clients", len(clients)))
		result.Conflicts = 1
		return result, err
	}

	rc := clients[0]

	repaired := false
	current, err := tx.ClientTrafficByID(ctx, rc.TrafficID)
	if errors.Is(err, store.ErrNotFound) {
		// 3x-ui 3.x replaces the email-keyed traffic row on upgrade or rename.
		// Follow the email to the current row before declaring the rule dead.
		replacement, emailErr := tx.ClientTrafficByEmail(ctx, rc.Email)
		if errors.Is(emailErr, store.ErrNotFound) {
			if err := markRuleClientMissing(ctx, tx, rule.ID, rc, now, dryRun); err != nil {
				return ReconcileResult{}, err
			}
			return orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("traffic id %d is missing", rc.TrafficID))
		}
		if emailErr != nil {
			return ReconcileResult{}, emailErr
		}
		if dryRun {
			return ReconcileResult{Reconciled: 1}, nil
		}
		relinked, linkErr := tx.RelinkRuleClientByEmail(ctx, rule.ID, rc.TrafficID, now)
		if linkErr != nil {
			if err := markRuleClientMissing(ctx, tx, rule.ID, rc, now, dryRun); err != nil {
				return ReconcileResult{}, err
			}
			return orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("traffic id %d could not follow email %s", rc.TrafficID, rc.Email))
		}
		message := fmt.Sprintf("traffic id %d relinked to traffic id %d email %s", rc.TrafficID, relinked.TrafficID, relinked.Email)
		if err := tx.AddEvent(ctx, &rule.ID, store.EventClientRelinked, message, now); err != nil {
			return ReconcileResult{}, err
		}
		rc = relinked
		current = replacement
		repaired = true
		err = nil
	}
	if err != nil {
		return ReconcileResult{}, err
	}
	if current.Email != rc.Email {
		if err := markRuleClientMissing(ctx, tx, rule.ID, rc, now, dryRun); err != nil {
			return ReconcileResult{}, err
		}
		return orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("traffic id %d no longer matches email identity", rc.TrafficID))
	}
	if rc.MissingAt != nil {
		if dryRun {
			return ReconcileResult{Reconciled: 1}, nil
		}
		if err := tx.UpdateRuleClientBaseline(ctx, store.RuleClient{
			RuleID:    rule.ID,
			TrafficID: rc.TrafficID,
			LastUp:    current.Up,
			LastDown:  current.Down,
			UpdatedAt: now,
		}); err != nil {
			return ReconcileResult{}, err
		}
		rc.MissingAt = nil
		message := fmt.Sprintf("traffic id %d revived; baseline refreshed", rc.TrafficID)
		if err := tx.AddEvent(ctx, &rule.ID, store.EventClientReset, message, now); err != nil {
			return ReconcileResult{}, err
		}
		repaired = true
	}
	if current.InboundID != rc.InboundID {
		if !dryRun {
			if err := tx.AdoptRuleClientInbound(ctx, rule.ID, rc.TrafficID, current.InboundID, now); err != nil {
				return ReconcileResult{}, err
			}
		}
		rc.InboundID = current.InboundID
	}

	scope, ok, err := compatibleScopeForClient(ctx, tx, scopes, rule, current)
	if err != nil {
		return ReconcileResult{}, err
	}
	if ok {
		adopted, err := adoptOrSupersede(ctx, tx, scope, rule, current, now, dryRun)
		if err != nil {
			return ReconcileResult{}, err
		}
		if adopted {
			return ReconcileResult{Reconciled: 1, Superseded: 1}, nil
		}
	}

	if current.Enable != 1 {
		result, err := orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("traffic id %d is disabled and no compatible include-disabled scope owns it", rc.TrafficID))
		result.DisabledClients = 1
		return result, err
	}

	if conflict, err := tx.ActiveScopeForTrafficExcept(ctx, current.ID, rule.ID); err == nil {
		result, orphanErr := orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("traffic id %d already has active scope rule %d", current.ID, conflict.ID))
		result.Conflicts = 1
		return result, orphanErr
	} else if !errors.Is(err, store.ErrNotFound) {
		return ReconcileResult{}, err
	}

	if repaired {
		return ReconcileResult{Reconciled: 1}, nil
	}
	return ReconcileResult{}, nil
}

func compatibleScopeForClient(ctx context.Context, tx *store.Tx, scopes []store.Scope, rule store.Rule, client store.ClientTraffic) (store.Scope, bool, error) {
	for _, scope := range scopes {
		if scope.RuleID == rule.ID {
			continue
		}
		matches, err := scopeMatchesClient(ctx, tx, scope, rule, client)
		if err != nil {
			return store.Scope{}, false, err
		}
		if !matches {
			continue
		}
		if conflict, err := tx.ActiveScopeForTrafficExcept(ctx, client.ID, scope.RuleID); err == nil && conflict.ID != scope.RuleID {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return store.Scope{}, false, err
		}
		return scope, true, nil
	}
	return store.Scope{}, false, nil
}

func scopeMatchesClient(ctx context.Context, tx *store.Tx, scope store.Scope, rule store.Rule, client store.ClientTraffic) (bool, error) {
	if scope.Once {
		return false, nil
	}
	if rule.FactorPPM != scope.Rule.FactorPPM {
		return false, nil
	}
	if scope.InboundID != nil {
		// The scope selects clients through the authoritative inbound link, and
		// only clients actually attached to that inbound may join it.
		member, err := tx.ClientIsInboundMember(ctx, client.Email, *scope.InboundID)
		if err != nil {
			return false, err
		}
		if !member {
			return false, nil
		}
	}
	if scope.LimitedOnly && client.Total <= 0 {
		return false, nil
	}
	if !scope.IncludeDisabledClients && client.Enable != 1 {
		return false, nil
	}
	return true, nil
}

func adoptOrSupersede(ctx context.Context, tx *store.Tx, scope store.Scope, rule store.Rule, client store.ClientTraffic, now int64, dryRun bool) (bool, error) {
	exists, err := tx.RuleClientExists(ctx, scope.RuleID, client.ID)
	if err != nil {
		return false, err
	}
	if dryRun {
		return true, nil
	}
	if exists {
		if err := tx.SetRuleMerged(ctx, rule.ID, now); err != nil {
			return false, err
		}
		message := fmt.Sprintf("rule superseded by scope %d", scope.RuleID)
		if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleMerged, message, now); err != nil {
			return false, err
		}
		return true, nil
	}
	adopted, reason, err := adoptSingleRuleIntoScope(ctx, tx, scope, rule, client, now)
	if err != nil {
		return false, err
	}
	if !adopted && reason != "" {
		if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleSkip, reason, now); err != nil {
			return false, err
		}
	}
	return adopted, nil
}

func orphanRule(ctx context.Context, tx *store.Tx, rule store.Rule, now int64, dryRun bool, reason string) (ReconcileResult, error) {
	if dryRun {
		return ReconcileResult{Reconciled: 1, Orphaned: 1}, nil
	}
	if err := tx.SetRuleOrphaned(ctx, rule.ID, now); err != nil {
		return ReconcileResult{}, err
	}
	message := "rule orphaned by reconcile"
	if reason != "" {
		message = "rule orphaned by reconcile: " + reason
	}
	if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleReconcile, message, now); err != nil {
		return ReconcileResult{}, err
	}
	return ReconcileResult{Reconciled: 1, Orphaned: 1}, nil
}

func markRuleClientMissing(ctx context.Context, tx *store.Tx, ruleID int64, rc store.RuleClient, now int64, dryRun bool) error {
	if rc.MissingAt != nil || dryRun {
		return nil
	}
	if err := tx.MarkRuleClientMissing(ctx, ruleID, rc.TrafficID, now); err != nil {
		return err
	}
	message := fmt.Sprintf("traffic id %d is missing or no longer matches rule target", rc.TrafficID)
	return tx.AddEvent(ctx, &ruleID, store.EventClientMissing, message, now)
}
