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
	Checked         int
	Reconciled      int
	Orphaned        int
	DisabledClients int
	Superseded      int
	Conflicts       int
}

func (r ReconcileResult) Summary() string {
	return fmt.Sprintf("checked=%d reconciled=%d orphaned=%d disabled_clients=%d superseded=%d conflicts=%d",
		r.Checked,
		r.Reconciled,
		r.Orphaned,
		r.DisabledClients,
		r.Superseded,
		r.Conflicts,
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
	return result, nil
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
	if rc.MissingAt != nil {
		return orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("traffic id %d is already marked missing", rc.TrafficID))
	}

	current, err := tx.ClientTrafficByID(ctx, rc.TrafficID)
	if errors.Is(err, store.ErrNotFound) {
		if err := markRuleClientMissing(ctx, tx, rule.ID, rc, now, dryRun); err != nil {
			return ReconcileResult{}, err
		}
		return orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("traffic id %d is missing", rc.TrafficID))
	}
	if err != nil {
		return ReconcileResult{}, err
	}
	if current.InboundID != rc.InboundID || current.Email != rc.Email {
		if err := markRuleClientMissing(ctx, tx, rule.ID, rc, now, dryRun); err != nil {
			return ReconcileResult{}, err
		}
		return orphanRule(ctx, tx, rule, now, dryRun, fmt.Sprintf("traffic id %d no longer matches inbound/email identity", rc.TrafficID))
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

	return ReconcileResult{}, nil
}

func compatibleScopeForClient(ctx context.Context, tx *store.Tx, scopes []store.Scope, rule store.Rule, client store.ClientTraffic) (store.Scope, bool, error) {
	for _, scope := range scopes {
		if scope.RuleID == rule.ID {
			continue
		}
		if !scopeMatchesClient(scope, rule, client) {
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

func scopeMatchesClient(scope store.Scope, rule store.Rule, client store.ClientTraffic) bool {
	if scope.Once {
		return false
	}
	if rule.FactorPPM != scope.Rule.FactorPPM {
		return false
	}
	if scope.InboundID != nil && client.InboundID != *scope.InboundID {
		return false
	}
	if scope.InboundID != nil && rule.InboundID != *scope.InboundID {
		return false
	}
	if scope.LimitedOnly && client.Total <= 0 {
		return false
	}
	if !scope.IncludeDisabledClients && client.Enable != 1 {
		return false
	}
	return true
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
