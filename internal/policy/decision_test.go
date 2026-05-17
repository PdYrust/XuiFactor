package policy

import (
	"errors"
	"testing"

	"github.com/PdYrust/XuiFactor/internal/store"
)

func TestDecideReturnsOneResultPerClient(t *testing.T) {
	decisions, err := Decide([]Candidate{
		candidate(1, 10, SourceGlobalScope, 2_000_000),
		candidate(2, 20, SourceGlobalScope, 3_000_000),
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(decisions) != 2 {
		t.Fatalf("decisions = %d, want 2", len(decisions))
	}
	for _, decision := range decisions {
		if len(decision.Matched) != 1 || decision.Target == nil {
			t.Fatalf("decision = %#v, want one matched target", decision)
		}
	}
}

func TestInboundScopeWinsOverGlobalScope(t *testing.T) {
	decisions, err := Decide([]Candidate{
		candidate(1, 10, SourceGlobalScope, 5_000_000),
		candidate(1, 20, SourceInboundScope, 2_000_000),
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(decisions))
	}
	decision := decisions[0]
	if decision.SourceType != SourceInboundScope || decision.SourceRuleID != 20 || decision.FactorPPM != 2_000_000 {
		t.Fatalf("decision = %#v, want inbound scope rule 20", decision)
	}
	if len(decision.Matched) != 2 || len(decision.Suppressed) != 1 {
		t.Fatalf("decision = %#v, want matched and suppressed global source", decision)
	}
}

func TestFutureExcludeAndOverridePrecedenceCanBeRepresented(t *testing.T) {
	decisions, err := Decide([]Candidate{
		candidate(1, 10, SourceGlobalScope, 5_000_000),
		candidate(1, 20, SourceInboundScope, 3_000_000),
		candidate(1, 30, SourceUserOverride, 2_000_000),
		noTargetCandidate(1, 40, SourceExclude, 0),
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(decisions))
	}
	decision := decisions[0]
	if decision.SourceType != SourceExclude || decision.FactorPPM != 0 {
		t.Fatalf("decision = %#v, want exclude source with no factor", decision)
	}
	if decision.Target != nil {
		t.Fatalf("decision target = %#v, want no materialized target for exclude", decision.Target)
	}
	if len(decision.Matched) != 4 {
		t.Fatalf("matched = %d, want 4", len(decision.Matched))
	}
}

func TestExcludePolicyCandidatesHaveHighestPrecedence(t *testing.T) {
	target := candidate(1, 10, SourceInboundScope, 2_000_000)
	decisions, err := Decide(append([]Candidate{target}, ExcludeCandidatesForActiveTargets([]store.ExcludePolicy{
		{ID: 99, TrafficID: 1, InboundID: 1, Email: "user@example.com", State: store.StateActive},
	}, []store.ActiveRuleClient{*target.Target})...))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(decisions))
	}
	decision := decisions[0]
	if decision.SourceType != SourceExclude || decision.SourceRuleID != 99 || decision.Target != nil {
		t.Fatalf("decision = %#v, want exclude policy", decision)
	}
	if len(decision.Suppressed) != 1 {
		t.Fatalf("suppressed = %d, want one rule target", len(decision.Suppressed))
	}
}

func TestExcludePolicyUsesFullClientIdentity(t *testing.T) {
	target := candidate(1, 10, SourceInboundScope, 2_000_000)
	candidates := ExcludeCandidatesForActiveTargets([]store.ExcludePolicy{
		{ID: 99, TrafficID: 1, InboundID: 1, Email: "other@example.com", State: store.StateActive},
		{ID: 100, TrafficID: 2, InboundID: 1, Email: "user@example.com", State: store.StateActive},
	}, []store.ActiveRuleClient{*target.Target})
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want no identity mismatch candidates", candidates)
	}
}

func TestOverridePolicyWinsOverInboundScope(t *testing.T) {
	target := candidate(1, 10, SourceInboundScope, 2_000_000)
	decisions, err := Decide(append([]Candidate{target}, OverrideCandidatesForActiveTargets([]store.OverridePolicy{
		{ID: 77, TrafficID: 1, InboundID: 1, Email: "user@example.com", FactorPPM: 1_200_000, State: store.StateActive},
	}, []store.ActiveRuleClient{*target.Target})...))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(decisions))
	}
	decision := decisions[0]
	if decision.SourceType != SourceUserOverride || decision.SourceRuleID != 77 || decision.FactorPPM != 1_200_000 {
		t.Fatalf("decision = %#v, want override policy", decision)
	}
	if decision.Target == nil || decision.Target.Rule.FactorPPM != 1_200_000 {
		t.Fatalf("decision target = %#v, want override factor target", decision.Target)
	}
	if len(decision.Suppressed) != 0 {
		t.Fatalf("suppressed = %d, want no duplicate suppression for shared materialized target", len(decision.Suppressed))
	}
}

func TestExcludeWinsOverOverride(t *testing.T) {
	target := candidate(1, 10, SourceInboundScope, 2_000_000)
	candidates := []Candidate{target}
	candidates = append(candidates, OverrideCandidatesForActiveTargets([]store.OverridePolicy{
		{ID: 77, TrafficID: 1, InboundID: 1, Email: "user@example.com", FactorPPM: 1_200_000, State: store.StateActive},
	}, []store.ActiveRuleClient{*target.Target})...)
	candidates = append(candidates, ExcludeCandidatesForActiveTargets([]store.ExcludePolicy{
		{ID: 88, TrafficID: 1, InboundID: 1, Email: "user@example.com", State: store.StateActive},
	}, []store.ActiveRuleClient{*target.Target})...)
	decisions, err := Decide(candidates)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	decision := decisions[0]
	if decision.SourceType != SourceExclude || decision.Target != nil || decision.FactorPPM != 0 {
		t.Fatalf("decision = %#v, want exclude no-factor decision", decision)
	}
	if len(decision.Suppressed) != 1 {
		t.Fatalf("suppressed = %d, want one materialized target", len(decision.Suppressed))
	}
}

func TestOverridePolicyUsesFullClientIdentity(t *testing.T) {
	target := candidate(1, 10, SourceInboundScope, 2_000_000)
	candidates := OverrideCandidatesForActiveTargets([]store.OverridePolicy{
		{ID: 77, TrafficID: 1, InboundID: 1, Email: "other@example.com", FactorPPM: 1_200_000, State: store.StateActive},
		{ID: 78, TrafficID: 2, InboundID: 1, Email: "user@example.com", FactorPPM: 1_200_000, State: store.StateActive},
	}, []store.ActiveRuleClient{*target.Target})
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want no identity mismatch candidates", candidates)
	}
}

func TestAmbiguousSamePrecedenceTargetsFailSafely(t *testing.T) {
	_, err := Decide([]Candidate{
		candidate(1, 10, SourceInboundScope, 2_000_000),
		candidate(1, 20, SourceInboundScope, 3_000_000),
	})
	if !errors.Is(err, ErrAmbiguousEffectiveTarget) {
		t.Fatalf("expected ambiguous effective target error, got %v", err)
	}
}

func candidate(trafficID, ruleID int64, source SourceType, factorPPM int64) Candidate {
	target := store.ActiveRuleClient{
		Rule: store.Rule{
			ID:        ruleID,
			FactorPPM: factorPPM,
		},
		Client: store.RuleClient{
			RuleID:    ruleID,
			TrafficID: trafficID,
			InboundID: 1,
			Email:     "user@example.com",
		},
	}
	return Candidate{
		Target:     &target,
		SourceType: source,
		RuleID:     ruleID,
		TrafficID:  trafficID,
		InboundID:  1,
		Email:      "user@example.com",
		FactorPPM:  factorPPM,
		Reason:     string(source),
	}
}

func noTargetCandidate(trafficID, ruleID int64, source SourceType, factorPPM int64) Candidate {
	return Candidate{
		SourceType: source,
		RuleID:     ruleID,
		TrafficID:  trafficID,
		InboundID:  1,
		Email:      "user@example.com",
		FactorPPM:  factorPPM,
		Reason:     string(source),
	}
}
