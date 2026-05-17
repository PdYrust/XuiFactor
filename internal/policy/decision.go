package policy

import (
	"errors"
	"fmt"
	"sort"

	"github.com/PdYrust/XuiFactor/internal/store"
)

type SourceType string

const (
	SourceNone         SourceType = "none"
	SourceExclude      SourceType = "exclude"
	SourceUserOverride SourceType = "user_override"
	SourceSingleRule   SourceType = "single_rule"
	SourceInboundScope SourceType = "inbound_scope"
	SourceGlobalScope  SourceType = "global_scope"
	SourceSnapshot     SourceType = "snapshot_scope"
)

type Match struct {
	SourceType SourceType
	RuleID     int64
	InboundID  int64
	Email      string
	FactorPPM  int64
	Reason     string
}

type Candidate struct {
	Target     *store.ActiveRuleClient
	SourceType SourceType
	RuleID     int64
	TrafficID  int64
	InboundID  int64
	Email      string
	FactorPPM  int64
	Reason     string
}

type Decision struct {
	TrafficID    int64
	InboundID    int64
	Email        string
	SourceType   SourceType
	SourceRuleID int64
	FactorPPM    int64
	Target       *store.ActiveRuleClient
	Matched      []Match
	Reason       string
	Suppressed   []store.ActiveRuleClient
}

var ErrAmbiguousEffectiveTarget = errors.New("ambiguous effective target")

const PrecedenceDescription = "exclude > override > single-user rule > inbound scope > global scope"

func ActiveRuleCandidates(targets []store.ActiveRuleClient) []Candidate {
	candidates := make([]Candidate, 0, len(targets))
	for _, target := range targets {
		target := target
		source, reason := classifyTarget(target)
		candidates = append(candidates, Candidate{
			Target:     &target,
			SourceType: source,
			RuleID:     target.Rule.ID,
			TrafficID:  target.Client.TrafficID,
			InboundID:  target.Client.InboundID,
			Email:      target.Client.Email,
			FactorPPM:  target.Rule.FactorPPM,
			Reason:     reason,
		})
	}
	return candidates
}

func ClientCandidates(client store.ClientTraffic, targets []store.ActiveRuleClient, excludes []store.ExcludePolicy, overrides []store.OverridePolicy) []Candidate {
	matchingTargets := make([]store.ActiveRuleClient, 0, 2)
	for _, target := range targets {
		if target.Client.TrafficID == client.ID && target.Client.InboundID == client.InboundID && target.Client.Email == client.Email {
			matchingTargets = append(matchingTargets, target)
		}
	}

	candidates := ActiveRuleCandidates(matchingTargets)
	overrideCandidates := OverrideCandidatesForActiveTargets(overrides, matchingTargets)
	candidates = append(candidates, overrideCandidates...)
	excludeCandidates := ExcludeCandidatesForActiveTargets(excludes, matchingTargets)
	candidates = append(candidates, excludeCandidates...)

	if len(excludeCandidates) == 0 {
		for _, exclude := range excludes {
			if exactPolicyMatch(client, exclude.TrafficID, exclude.InboundID, exclude.Email) {
				candidates = append(candidates, Candidate{
					SourceType: SourceExclude,
					RuleID:     exclude.ID,
					TrafficID:  exclude.TrafficID,
					InboundID:  exclude.InboundID,
					Email:      exclude.Email,
					FactorPPM:  0,
					Reason:     "exclude policy",
				})
				break
			}
		}
	}
	if len(overrideCandidates) == 0 {
		for _, override := range overrides {
			if exactPolicyMatch(client, override.TrafficID, override.InboundID, override.Email) {
				candidates = append(candidates, Candidate{
					SourceType: SourceUserOverride,
					RuleID:     override.ID,
					TrafficID:  override.TrafficID,
					InboundID:  override.InboundID,
					Email:      override.Email,
					FactorPPM:  override.FactorPPM,
					Reason:     "user override policy without active rule target",
				})
				break
			}
		}
	}
	return candidates
}

func DecideClient(client store.ClientTraffic, candidates []Candidate) (Decision, error) {
	if len(candidates) == 0 {
		return Decision{
			TrafficID:  client.ID,
			InboundID:  client.InboundID,
			Email:      client.Email,
			SourceType: SourceNone,
			FactorPPM:  0,
			Reason:     "no active matching rule, scope, override, or exclude",
		}, nil
	}
	return decideOne(client.ID, candidates)
}

func DecideActiveRuleClients(targets []store.ActiveRuleClient) ([]Decision, error) {
	return Decide(ActiveRuleCandidates(targets))
}

func ExcludeCandidatesForActiveTargets(excludes []store.ExcludePolicy, targets []store.ActiveRuleClient) []Candidate {
	type targetKey struct {
		trafficID int64
		inboundID int64
		email     string
	}
	targetKeys := make(map[targetKey]struct{}, len(targets))
	for _, target := range targets {
		targetKeys[targetKey{
			trafficID: target.Client.TrafficID,
			inboundID: target.Client.InboundID,
			email:     target.Client.Email,
		}] = struct{}{}
	}

	candidates := make([]Candidate, 0, len(excludes))
	for _, exclude := range excludes {
		key := targetKey{
			trafficID: exclude.TrafficID,
			inboundID: exclude.InboundID,
			email:     exclude.Email,
		}
		if _, ok := targetKeys[key]; !ok {
			continue
		}
		candidates = append(candidates, Candidate{
			SourceType: SourceExclude,
			RuleID:     exclude.ID,
			TrafficID:  exclude.TrafficID,
			InboundID:  exclude.InboundID,
			Email:      exclude.Email,
			FactorPPM:  0,
			Reason:     "exclude policy",
		})
	}
	return candidates
}

func OverrideCandidatesForActiveTargets(overrides []store.OverridePolicy, targets []store.ActiveRuleClient) []Candidate {
	type targetKey struct {
		trafficID int64
		inboundID int64
		email     string
	}
	targetsByKey := make(map[targetKey]store.ActiveRuleClient, len(targets))
	for _, target := range targets {
		targetsByKey[targetKey{
			trafficID: target.Client.TrafficID,
			inboundID: target.Client.InboundID,
			email:     target.Client.Email,
		}] = target
	}

	candidates := make([]Candidate, 0, len(overrides))
	for _, override := range overrides {
		key := targetKey{
			trafficID: override.TrafficID,
			inboundID: override.InboundID,
			email:     override.Email,
		}
		target, ok := targetsByKey[key]
		if !ok {
			continue
		}
		target.Rule.FactorPPM = override.FactorPPM
		candidates = append(candidates, Candidate{
			Target:     &target,
			SourceType: SourceUserOverride,
			RuleID:     override.ID,
			TrafficID:  override.TrafficID,
			InboundID:  override.InboundID,
			Email:      override.Email,
			FactorPPM:  override.FactorPPM,
			Reason:     "user override policy",
		})
	}
	return candidates
}

func exactPolicyMatch(client store.ClientTraffic, trafficID, inboundID int64, email string) bool {
	return client.ID == trafficID && client.InboundID == inboundID && client.Email == email
}

func Decide(candidates []Candidate) ([]Decision, error) {
	groups := make(map[int64][]Candidate)
	for _, candidate := range candidates {
		groups[candidate.TrafficID] = append(groups[candidate.TrafficID], candidate)
	}

	trafficIDs := make([]int64, 0, len(groups))
	for trafficID := range groups {
		trafficIDs = append(trafficIDs, trafficID)
	}
	sort.Slice(trafficIDs, func(i, j int) bool { return trafficIDs[i] < trafficIDs[j] })

	decisions := make([]Decision, 0, len(trafficIDs))
	for _, trafficID := range trafficIDs {
		decision, err := decideOne(trafficID, groups[trafficID])
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, nil
}

func decideOne(trafficID int64, candidates []Candidate) (Decision, error) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := precedence(candidates[i].SourceType)
		right := precedence(candidates[j].SourceType)
		if left != right {
			return left > right
		}
		return candidates[i].RuleID < candidates[j].RuleID
	})

	winner := candidates[0]
	if len(candidates) > 1 && precedence(candidates[0].SourceType) == precedence(candidates[1].SourceType) {
		return Decision{}, fmt.Errorf("%w: traffic id %d has multiple %s candidates", ErrAmbiguousEffectiveTarget, trafficID, candidates[0].SourceType)
	}

	matches := make([]Match, 0, len(candidates))
	suppressed := make([]store.ActiveRuleClient, 0, len(candidates)-1)
	suppressedKeys := make(map[materializedTargetKey]struct{}, len(candidates)-1)
	for i, candidate := range candidates {
		matches = append(matches, Match{
			SourceType: candidate.SourceType,
			RuleID:     candidate.RuleID,
			InboundID:  candidate.InboundID,
			Email:      candidate.Email,
			FactorPPM:  candidate.FactorPPM,
			Reason:     candidate.Reason,
		})
		if i > 0 && candidate.Target != nil && !sameMaterializedTarget(winner.Target, candidate.Target) {
			key := materializedKey(candidate.Target)
			if _, ok := suppressedKeys[key]; ok {
				continue
			}
			suppressedKeys[key] = struct{}{}
			suppressed = append(suppressed, *candidate.Target)
		}
	}

	return Decision{
		TrafficID:    winner.TrafficID,
		InboundID:    winner.InboundID,
		Email:        winner.Email,
		SourceType:   winner.SourceType,
		SourceRuleID: winner.RuleID,
		FactorPPM:    winner.FactorPPM,
		Target:       winner.Target,
		Matched:      matches,
		Reason:       winner.Reason,
		Suppressed:   suppressed,
	}, nil
}

type materializedTargetKey struct {
	ruleID    int64
	trafficID int64
}

func sameMaterializedTarget(left, right *store.ActiveRuleClient) bool {
	if left == nil || right == nil {
		return false
	}
	return materializedKey(left) == materializedKey(right)
}

func materializedKey(target *store.ActiveRuleClient) materializedTargetKey {
	return materializedTargetKey{
		ruleID:    target.Client.RuleID,
		trafficID: target.Client.TrafficID,
	}
}

func classifyTarget(target store.ActiveRuleClient) (SourceType, string) {
	scope := target.Rule.Scope
	if scope == nil {
		return SourceSingleRule, "single-user rule"
	}
	if scope.Once {
		return SourceSnapshot, "snapshot scope"
	}
	if scope.InboundID != nil {
		return SourceInboundScope, "inbound persistent scope"
	}
	return SourceGlobalScope, "global persistent scope"
}

func precedence(source SourceType) int {
	switch source {
	case SourceExclude:
		return 100
	case SourceUserOverride:
		return 90
	case SourceSingleRule:
		return 80
	case SourceInboundScope:
		return 70
	case SourceSnapshot:
		return 65
	case SourceGlobalScope:
		return 60
	default:
		return 0
	}
}
