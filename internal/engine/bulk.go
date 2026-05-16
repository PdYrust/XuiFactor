package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/PdYrust/XuiFactor/internal/store"
)

type EnableAllRequest struct {
	Factor                 string
	InboundID              *int64
	LimitedOnly            bool
	IncludeDisabledClients bool
	Once                   bool
	Name                   string
}

type BulkSelector struct {
	InboundID *int64
}

type BulkResult struct {
	Matched         int
	Changed         int
	Adopted         int
	SkippedExisting int
	Conflicts       int
	Missing         int
	Mode            string
}

func (r BulkResult) Summary() string {
	prefix := ""
	if r.Mode != "" {
		prefix = "mode=" + r.Mode + " "
	}
	return fmt.Sprintf("%smatched=%d changed=%d adopted=%d skipped=%d conflicts=%d missing=%d",
		prefix,
		r.Matched,
		r.Changed,
		r.Adopted,
		r.SkippedExisting,
		r.Conflicts,
		r.Missing,
	)
}

func (s *Service) EnableAll(ctx context.Context, req EnableAllRequest) (BulkResult, error) {
	factorPPM, err := ParseFactor(req.Factor)
	if err != nil {
		return BulkResult{}, err
	}
	now := s.now().Unix()
	result := BulkResult{Mode: "persistent"}
	if req.Once {
		result.Mode = "snapshot"
	}

	err = s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		clients, err := tx.ListClientCandidates(ctx, store.ClientFilter{
			InboundID:              req.InboundID,
			LimitedOnly:            req.LimitedOnly,
			IncludeDisabledClients: req.IncludeDisabledClients,
		})
		if err != nil {
			return err
		}
		if len(clients) == 0 {
			return fmt.Errorf("%w: enable-all found no matching clients", store.ErrNotFound)
		}
		result.Matched = len(clients)
		if err := rejectDuplicateClientIDs(clients); err != nil {
			result.Conflicts++
			return err
		}

		if req.Once {
			return s.enableAllSnapshot(ctx, tx, req, factorPPM, clients, now, &result)
		}
		return s.enableAllPersistent(ctx, tx, req, factorPPM, clients, now, &result)
	})
	return result, err
}

func (s *Service) enableAllSnapshot(ctx context.Context, tx *store.Tx, req EnableAllRequest, factorPPM int64, clients []store.ClientTraffic, now int64, result *BulkResult) error {
	type createTarget struct {
		client store.ClientTraffic
	}
	targets := make([]createTarget, 0, len(clients))
	for _, client := range clients {
		rules, err := tx.RulesForTrafficInStates(ctx, client.ID, store.StateActive, store.StatePaused)
		if err != nil {
			return err
		}
		if len(rules) > 1 {
			result.Conflicts++
			return store.ConflictError("multiple active or paused rules already target traffic id %d", client.ID)
		}
		if len(rules) == 1 {
			result.SkippedExisting++
			continue
		}
		targets = append(targets, createTarget{client: client})
	}
	if len(targets) == 0 {
		return nil
	}

	ruleID, err := tx.CreateRule(ctx, store.Rule{
		Name:        strings.TrimSpace(req.Name),
		InboundID:   scopedRuleInboundID(req.InboundID),
		Email:       "",
		FactorPPM:   factorPPM,
		State:       store.StateActive,
		CreatedAt:   now,
		UpdatedAt:   now,
		ActivatedAt: &now,
	})
	if err != nil {
		return err
	}
	if err := tx.CreateScope(ctx, store.Scope{
		RuleID:                 ruleID,
		InboundID:              req.InboundID,
		LimitedOnly:            req.LimitedOnly,
		IncludeDisabledClients: req.IncludeDisabledClients,
		Once:                   true,
		CreatedAt:              now,
		UpdatedAt:              now,
	}); err != nil {
		return err
	}
	if err := tx.AddEvent(ctx, &ruleID, store.EventRuleEnabled, "snapshot scope enabled by enable-all", now); err != nil {
		return err
	}

	for _, target := range targets {
		if err := tx.CreateRuleClient(ctx, store.RuleClient{
			RuleID:      ruleID,
			TrafficID:   target.client.ID,
			InboundID:   target.client.InboundID,
			Email:       target.client.Email,
			LastUp:      target.client.Up,
			LastDown:    target.client.Down,
			LastAllTime: target.client.AllTime,
			UpdatedAt:   now,
		}); err != nil {
			return err
		}
		result.Changed++
	}
	return nil
}

func (s *Service) enableAllPersistent(ctx context.Context, tx *store.Tx, req EnableAllRequest, factorPPM int64, clients []store.ClientTraffic, now int64, result *BulkResult) error {
	scope, err := tx.FindActivePersistentScope(ctx, factorPPM, req.InboundID, req.LimitedOnly, req.IncludeDisabledClients)
	if errors.Is(err, store.ErrNotFound) {
		ruleID, err := tx.CreateRule(ctx, store.Rule{
			Name:        strings.TrimSpace(req.Name),
			InboundID:   scopedRuleInboundID(req.InboundID),
			Email:       "",
			FactorPPM:   factorPPM,
			State:       store.StateActive,
			CreatedAt:   now,
			UpdatedAt:   now,
			ActivatedAt: &now,
		})
		if err != nil {
			return err
		}
		if err := tx.CreateScope(ctx, store.Scope{
			RuleID:                 ruleID,
			InboundID:              req.InboundID,
			LimitedOnly:            req.LimitedOnly,
			IncludeDisabledClients: req.IncludeDisabledClients,
			Once:                   false,
			CreatedAt:              now,
			UpdatedAt:              now,
		}); err != nil {
			return err
		}
		if err := tx.AddEvent(ctx, &ruleID, store.EventRuleEnabled, "persistent scope enabled by enable-all", now); err != nil {
			return err
		}
		scope = store.Scope{
			RuleID:                 ruleID,
			InboundID:              req.InboundID,
			LimitedOnly:            req.LimitedOnly,
			IncludeDisabledClients: req.IncludeDisabledClients,
			Once:                   false,
			CreatedAt:              now,
			UpdatedAt:              now,
			Rule: store.Rule{
				ID:        ruleID,
				InboundID: scopedRuleInboundID(req.InboundID),
				FactorPPM: factorPPM,
				State:     store.StateActive,
			},
		}
	} else if err != nil {
		return err
	}

	for _, client := range clients {
		exists, err := tx.RuleClientExists(ctx, scope.RuleID, client.ID)
		if err != nil {
			return err
		}
		if exists {
			result.SkippedExisting++
			continue
		}

		rules, err := tx.RulesForTrafficInStates(ctx, client.ID, store.StateActive, store.StatePaused)
		if err != nil {
			return err
		}
		if len(rules) > 1 {
			result.Conflicts++
			return store.ConflictError("multiple active or paused rules already target traffic id %d", client.ID)
		}
		if len(rules) == 0 {
			if err := tx.CreateRuleClient(ctx, store.RuleClient{
				RuleID:      scope.RuleID,
				TrafficID:   client.ID,
				InboundID:   client.InboundID,
				Email:       client.Email,
				LastUp:      client.Up,
				LastDown:    client.Down,
				LastAllTime: client.AllTime,
				UpdatedAt:   now,
			}); err != nil {
				return err
			}
			result.Changed++
			continue
		}

		rule := rules[0]
		hasScope, err := tx.RuleHasScope(ctx, rule.ID)
		if err != nil {
			return err
		}
		if hasScope {
			result.Conflicts++
			message := fmt.Sprintf("traffic id %d skipped; active scope rule %d already targets it", client.ID, rule.ID)
			if err := tx.AddEvent(ctx, &scope.RuleID, store.EventScopeSkip, message, now); err != nil {
				return err
			}
			continue
		}
		if rule.State != store.StateActive {
			result.SkippedExisting++
			if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleSkip, "rule is not active", now); err != nil {
				return err
			}
			continue
		}

		adopted, reason, err := s.adoptSingleRuleIntoScope(ctx, tx, scope, rule, client, now)
		if err != nil {
			return err
		}
		if adopted {
			result.Adopted++
			continue
		}
		result.SkippedExisting++
		if reason != "" {
			if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleSkip, reason, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) adoptSingleRuleIntoScope(ctx context.Context, tx *store.Tx, scope store.Scope, rule store.Rule, client store.ClientTraffic, now int64) (bool, string, error) {
	if rule.FactorPPM != scope.Rule.FactorPPM {
		return false, "factor does not match scope", nil
	}
	if scope.InboundID != nil && rule.InboundID != *scope.InboundID {
		return false, "inbound does not match scope", nil
	}
	if scope.LimitedOnly && client.Total <= 0 {
		return false, "client is not limited", nil
	}
	if !scope.IncludeDisabledClients && client.Enable != 1 {
		return false, "client is disabled", nil
	}
	count, err := tx.RuleClientCount(ctx, rule.ID)
	if err != nil {
		return false, "", err
	}
	if count != 1 {
		return false, "rule does not have exactly one materialized client", nil
	}
	rc, err := tx.RuleClient(ctx, rule.ID, client.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, "rule client is missing", nil
	}
	if err != nil {
		return false, "", err
	}
	if rc.MissingAt != nil || rc.InboundID != client.InboundID || rc.Email != client.Email {
		if err := tx.MarkRuleClientMissing(ctx, rule.ID, rc.TrafficID, now); err != nil {
			return false, "", err
		}
		message := fmt.Sprintf("traffic id %d is missing or no longer matches rule target", rc.TrafficID)
		if err := tx.AddEvent(ctx, &rule.ID, store.EventClientMissing, message, now); err != nil {
			return false, "", err
		}
		return false, "rule client identity does not match", nil
	}
	if conflict, err := tx.ActiveScopeForTrafficExcept(ctx, client.ID, scope.RuleID); err == nil {
		return false, fmt.Sprintf("active scope %d already targets traffic id %d", conflict.ID, client.ID), nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, "", err
	}

	if err := tx.CopyRuleClient(ctx, rule.ID, scope.RuleID, client.ID); err != nil {
		return false, "", err
	}
	if err := tx.SetRuleMerged(ctx, rule.ID, now); err != nil {
		return false, "", err
	}
	message := fmt.Sprintf("traffic id %d adopted from rule %d", client.ID, rule.ID)
	if err := tx.AddEvent(ctx, &scope.RuleID, store.EventClientAdopted, message, now); err != nil {
		return false, "", err
	}
	message = fmt.Sprintf("rule merged into scope %d", scope.RuleID)
	if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleMerged, message, now); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func (s *Service) DisableAll(ctx context.Context, selector BulkSelector) (BulkResult, error) {
	return s.bulkTransition(ctx, selector, "disable-all", []string{store.StateActive, store.StatePaused}, store.StateDisabled)
}

func (s *Service) PauseAll(ctx context.Context, selector BulkSelector) (BulkResult, error) {
	return s.bulkTransition(ctx, selector, "pause-all", []string{store.StateActive}, store.StatePaused)
}

func (s *Service) ResumeAll(ctx context.Context, selector BulkSelector) (BulkResult, error) {
	now := s.now().Unix()
	var result BulkResult

	err := s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		rules, err := tx.ListRulesInStates(ctx, selector.InboundID, store.StatePaused)
		if err != nil {
			return err
		}
		if len(rules) == 0 {
			return fmt.Errorf("%w: resume-all found no matching paused rules", store.ErrNotFound)
		}
		result.Matched = len(rules)

		for _, rule := range rules {
			if conflict, trafficID, err := tx.ActiveConflictForRule(ctx, rule.ID); err == nil {
				result.Conflicts++
				message := fmt.Sprintf("resume skipped; active rule %d already targets traffic id %d", conflict.ID, trafficID)
				if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleResumed, message, now); err != nil {
					return err
				}
				continue
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}

			count, err := tx.RefreshRuleClientBaselinesCount(ctx, rule.ID, now)
			if err != nil {
				return err
			}
			if count == 0 && rule.Scope == nil {
				result.Missing++
				continue
			}
			if err := tx.SetRuleActive(ctx, rule.ID, now); err != nil {
				return err
			}
			if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleResumed, "rule resumed by resume-all", now); err != nil {
				return err
			}
			result.Changed++
		}
		return nil
	})
	return result, err
}

func scopedRuleInboundID(inboundID *int64) int64 {
	if inboundID == nil {
		return 0
	}
	return *inboundID
}

func (s *Service) bulkTransition(ctx context.Context, selector BulkSelector, command string, from []string, to string) (BulkResult, error) {
	now := s.now().Unix()
	var result BulkResult

	err := s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		rules, err := tx.ListRulesInStates(ctx, selector.InboundID, from...)
		if err != nil {
			return err
		}
		if len(rules) == 0 {
			return fmt.Errorf("%w: %s found no matching rules", store.ErrNotFound, command)
		}
		result.Matched = len(rules)

		for _, rule := range rules {
			switch to {
			case store.StateDisabled:
				if err := tx.SetRuleDisabled(ctx, rule.ID, now); err != nil {
					return err
				}
				if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleDisabled, "rule disabled by disable-all", now); err != nil {
					return err
				}
			case store.StatePaused:
				if err := tx.SetRulePaused(ctx, rule.ID, now); err != nil {
					return err
				}
				if err := tx.AddEvent(ctx, &rule.ID, store.EventRulePaused, "rule paused by pause-all", now); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported rule state %q", to)
			}
			result.Changed++
		}
		return nil
	})
	return result, err
}

func rejectDuplicateClientIDs(clients []store.ClientTraffic) error {
	seen := make(map[int64]struct{}, len(clients))
	for _, client := range clients {
		if _, ok := seen[client.ID]; ok {
			return store.ConflictError("duplicate selected client traffic id %d", client.ID)
		}
		seen[client.ID] = struct{}{}
	}
	return nil
}

func IsNoMatch(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}
