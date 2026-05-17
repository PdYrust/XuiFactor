package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/PdYrust/XuiFactor/internal/policy"
	"github.com/PdYrust/XuiFactor/internal/store"
)

const DefaultClientStatusLimit = 50

type ExplainRequest struct {
	Email     string
	InboundID *int64
}

type ExplainResult struct {
	Client   store.ClientTraffic
	Decision policy.Decision
	Baseline *store.RuleClient
}

type EffectiveStatusRequest struct {
	InboundID *int64
}

type EffectiveStatusResult struct {
	Rules                    []store.Rule
	Excludes                 []store.ExcludePolicy
	Overrides                []store.OverridePolicy
	Scopes                   int
	EffectiveFactoredClients int
	ExcludedClients          int
	OverriddenClients        int
}

type ClientStatusRequest struct {
	InboundID *int64
	Limit     int
}

type ClientStatusResult struct {
	Clients   []ClientEffectiveDecision
	Truncated bool
	Limit     int
}

type ClientEffectiveDecision struct {
	Client   store.ClientTraffic
	Decision policy.Decision
}

func (s *Service) Explain(ctx context.Context, req ExplainRequest) (ExplainResult, error) {
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return ExplainResult{}, err
	}
	if req.InboundID == nil {
		return ExplainResult{}, errors.New("inbound id is required")
	}

	var result ExplainResult
	err = s.store.WithReadTx(ctx, func(tx *store.Tx) error {
		client, err := tx.FindClient(ctx, email, req.InboundID)
		if err != nil {
			return describeSelectorError("find client", err)
		}
		decision, err := effectiveDecisionForClient(ctx, tx, client)
		if err != nil {
			return err
		}
		result.Client = client
		result.Decision = decision
		if decision.Target != nil {
			baseline := decision.Target.Client
			result.Baseline = &baseline
		}
		return nil
	})
	return result, err
}

func (s *Service) EffectiveStatus(ctx context.Context, req EffectiveStatusRequest) (EffectiveStatusResult, error) {
	rules, err := s.Status(ctx, false)
	if err != nil {
		return EffectiveStatusResult{}, err
	}
	excludes, err := s.Excludes(ctx, ExcludeListRequest{InboundID: req.InboundID})
	if err != nil {
		return EffectiveStatusResult{}, err
	}
	overrides, err := s.Overrides(ctx, OverrideListRequest{InboundID: req.InboundID})
	if err != nil {
		return EffectiveStatusResult{}, err
	}
	clients, err := s.ClientStatus(ctx, ClientStatusRequest{InboundID: req.InboundID})
	if err != nil {
		return EffectiveStatusResult{}, err
	}

	result := EffectiveStatusResult{
		Rules:     rules,
		Excludes:  excludes,
		Overrides: overrides,
	}
	for _, rule := range rules {
		if rule.Scope != nil && rule.State == store.StateActive && scopeMatchesInbound(rule.Scope, req.InboundID) {
			result.Scopes++
		}
	}
	for _, client := range clients.Clients {
		switch client.Decision.SourceType {
		case policy.SourceExclude:
			result.ExcludedClients++
		case policy.SourceUserOverride:
			if client.Decision.Target != nil {
				result.EffectiveFactoredClients++
			}
			result.OverriddenClients++
		case policy.SourceSingleRule, policy.SourceInboundScope, policy.SourceGlobalScope, policy.SourceSnapshot:
			if client.Decision.Target != nil {
				result.EffectiveFactoredClients++
			}
		}
	}
	return result, nil
}

func (s *Service) ClientStatus(ctx context.Context, req ClientStatusRequest) (ClientStatusResult, error) {
	limit := req.Limit
	if limit < 0 {
		return ClientStatusResult{}, errors.New("client status limit must not be negative")
	}
	var result ClientStatusResult
	result.Limit = limit
	err := s.store.WithReadTx(ctx, func(tx *store.Tx) error {
		clients, truncated, err := tx.ListClientTraffics(ctx, store.ClientListFilter{
			InboundID: req.InboundID,
			Limit:     limit,
		})
		if err != nil {
			return err
		}
		targets, excludes, overrides, err := effectiveInputs(ctx, tx)
		if err != nil {
			return err
		}
		result.Truncated = truncated
		result.Clients = make([]ClientEffectiveDecision, 0, len(clients))
		for _, client := range clients {
			decision, err := decideLoadedClient(client, targets, excludes, overrides)
			if err != nil {
				return err
			}
			result.Clients = append(result.Clients, ClientEffectiveDecision{
				Client:   client,
				Decision: decision,
			})
		}
		return nil
	})
	return result, err
}

func effectiveDecisionForClient(ctx context.Context, tx *store.Tx, client store.ClientTraffic) (policy.Decision, error) {
	targets, excludes, overrides, err := effectiveInputs(ctx, tx)
	if err != nil {
		return policy.Decision{}, err
	}
	return decideLoadedClient(client, targets, excludes, overrides)
}

func effectiveInputs(ctx context.Context, tx *store.Tx) ([]store.ActiveRuleClient, []store.ExcludePolicy, []store.OverridePolicy, error) {
	for _, table := range []string{"xui_factor_rules", "xui_factor_rule_clients", "xui_factor_scopes"} {
		exists, err := tx.TableExists(ctx, table)
		if err != nil {
			return nil, nil, nil, err
		}
		if !exists {
			return nil, nil, nil, nil
		}
	}
	targets, err := tx.ListActiveRuleClients(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	var excludes []store.ExcludePolicy
	excludesReady, err := tx.TableExists(ctx, "xui_factor_excludes")
	if err != nil {
		return nil, nil, nil, err
	}
	if excludesReady {
		excludes, err = tx.ListActiveExcludes(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	var overrides []store.OverridePolicy
	overridesReady, err := tx.TableExists(ctx, "xui_factor_overrides")
	if err != nil {
		return nil, nil, nil, err
	}
	if overridesReady {
		overrides, err = tx.ListActiveOverrides(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return targets, excludes, overrides, nil
}

func decideLoadedClient(client store.ClientTraffic, targets []store.ActiveRuleClient, excludes []store.ExcludePolicy, overrides []store.OverridePolicy) (policy.Decision, error) {
	validTargets := validTargetsForClient(client, targets)
	candidates := policy.ClientCandidates(client, validTargets, excludes, overrides)
	decision, err := policy.DecideClient(client, candidates)
	if err != nil {
		if errors.Is(err, policy.ErrAmbiguousEffectiveTarget) {
			return policy.Decision{}, store.ConflictError("%v", err)
		}
		return policy.Decision{}, err
	}
	return decision, nil
}

func validTargetsForClient(client store.ClientTraffic, targets []store.ActiveRuleClient) []store.ActiveRuleClient {
	valid := make([]store.ActiveRuleClient, 0, 2)
	for _, target := range targets {
		if target.Client.TrafficID != client.ID || target.Client.InboundID != client.InboundID || target.Client.Email != client.Email {
			continue
		}
		if target.Client.MissingAt != nil {
			continue
		}
		if !targetAllowsClient(target, client) {
			continue
		}
		valid = append(valid, target)
	}
	return valid
}

func targetAllowsClient(target store.ActiveRuleClient, client store.ClientTraffic) bool {
	if target.Rule.Scope == nil {
		return client.Enable == 1
	}
	return target.Rule.Scope.IncludeDisabledClients || client.Enable == 1
}

func scopeMatchesInbound(scope *store.Scope, inboundID *int64) bool {
	if inboundID == nil {
		return true
	}
	if scope.InboundID == nil {
		return true
	}
	return *scope.InboundID == *inboundID
}

func ExplainResultText(result ExplainResult) string {
	switch result.Decision.SourceType {
	case policy.SourceExclude:
		return "future traffic is not factored while exclude is active"
	case policy.SourceUserOverride:
		if result.Decision.Target == nil {
			return "override is active but no active rule or scope currently targets this client"
		}
		return fmt.Sprintf("future traffic uses factor %s", FormatFactor(result.Decision.FactorPPM))
	case policy.SourceSingleRule, policy.SourceInboundScope, policy.SourceGlobalScope, policy.SourceSnapshot:
		return fmt.Sprintf("future traffic uses factor %s", FormatFactor(result.Decision.FactorPPM))
	default:
		return "no active matching rule, scope, override, or exclude"
	}
}
