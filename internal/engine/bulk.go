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
	return fmt.Sprintf("%smatched=%d changed=%d skipped=%d conflicts=%d missing=%d",
		prefix,
		r.Matched,
		r.Changed,
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
		if req.Once && len(targets) == 0 {
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
			Once:                   req.Once,
			CreatedAt:              now,
			UpdatedAt:              now,
		}); err != nil {
			return err
		}

		eventMessage := "persistent scope enabled by enable-all"
		if req.Once {
			eventMessage = "snapshot scope enabled by enable-all"
		}
		if err := tx.AddEvent(ctx, &ruleID, store.EventRuleEnabled, eventMessage, now); err != nil {
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
	})
	return result, err
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
