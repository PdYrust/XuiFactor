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

func DecideActiveRuleClients(targets []store.ActiveRuleClient) ([]Decision, error) {
	return Decide(ActiveRuleCandidates(targets))
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
	for i, candidate := range candidates {
		matches = append(matches, Match{
			SourceType: candidate.SourceType,
			RuleID:     candidate.RuleID,
			InboundID:  candidate.InboundID,
			Email:      candidate.Email,
			FactorPPM:  candidate.FactorPPM,
			Reason:     candidate.Reason,
		})
		if i > 0 && candidate.Target != nil {
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
