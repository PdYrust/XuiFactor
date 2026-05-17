package engine

import (
	"context"

	"github.com/PdYrust/XuiFactor/internal/policy"
	"github.com/PdYrust/XuiFactor/internal/store"
)

type ReportRequest struct {
	InboundID  *int64
	IncludeAll bool
}

type ReportResult struct {
	MetadataReady            bool
	ActiveScopes             int
	ActiveSingleUserRules    int
	PausedRules              int
	DisabledRules            int
	ActiveExcludes           int
	InactiveExcludes         int
	ActiveOverrides          int
	InactiveOverrides        int
	EffectiveFactoredClients int
	ExcludedClients          int
	OverriddenClients        int
	NoFactorClients          int
	TotalActiveRuleClients   int
	TrafficImpact            store.TrafficImpact
}

func (s *Service) Report(ctx context.Context, req ReportRequest) (ReportResult, error) {
	ready, err := s.store.MetadataReady(ctx)
	if err != nil {
		return ReportResult{}, err
	}
	result := ReportResult{MetadataReady: ready}
	if !ready {
		return result, nil
	}

	effective, err := s.EffectiveStatus(ctx, EffectiveStatusRequest{InboundID: req.InboundID})
	if err != nil {
		return ReportResult{}, err
	}
	result.ActiveExcludes = len(effective.Excludes)
	result.ActiveOverrides = len(effective.Overrides)
	result.EffectiveFactoredClients = effective.EffectiveFactoredClients
	result.ExcludedClients = effective.ExcludedClients
	result.OverriddenClients = effective.OverriddenClients

	rules := effective.Rules
	if req.IncludeAll {
		rules, err = s.Status(ctx, true)
		if err != nil {
			return ReportResult{}, err
		}
	}
	for _, rule := range rules {
		if rule.Scope != nil && !scopeMatchesInbound(rule.Scope, req.InboundID) {
			continue
		}
		if req.InboundID != nil && rule.Scope == nil && rule.InboundID != *req.InboundID {
			continue
		}
		switch rule.State {
		case store.StateActive:
			if rule.Scope != nil {
				result.ActiveScopes++
			} else {
				result.ActiveSingleUserRules++
			}
		case store.StatePaused:
			result.PausedRules++
		case store.StateDisabled, store.StateMerged, store.StateOrphaned:
			result.DisabledRules++
		}
	}

	clients, err := s.ClientStatus(ctx, ClientStatusRequest{InboundID: req.InboundID})
	if err != nil {
		return ReportResult{}, err
	}
	for _, client := range clients.Clients {
		if client.Decision.SourceType == policy.SourceNone {
			result.NoFactorClients++
		}
	}

	activeRuleClients, err := s.store.CountActiveRuleClients(ctx, req.InboundID)
	if err != nil {
		return ReportResult{}, err
	}
	result.TotalActiveRuleClients = activeRuleClients

	if req.IncludeAll {
		excludes, err := s.Excludes(ctx, ExcludeListRequest{InboundID: req.InboundID, IncludeInactive: true})
		if err != nil {
			return ReportResult{}, err
		}
		for _, exclude := range excludes {
			if exclude.State != store.StateActive {
				result.InactiveExcludes++
			}
		}
		overrides, err := s.Overrides(ctx, OverrideListRequest{InboundID: req.InboundID, IncludeInactive: true})
		if err != nil {
			return ReportResult{}, err
		}
		for _, override := range overrides {
			if override.State != store.StateActive {
				result.InactiveOverrides++
			}
		}
	}

	impact, err := s.store.TrafficImpact(ctx, store.EventFilter{InboundID: req.InboundID})
	if err != nil {
		return ReportResult{}, err
	}
	result.TrafficImpact = impact
	return result, nil
}
