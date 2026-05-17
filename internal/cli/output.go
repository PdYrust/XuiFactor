package cli

import (
	"fmt"
	"io"
	"strconv"

	"github.com/PdYrust/XuiFactor/internal/engine"
	"github.com/PdYrust/XuiFactor/internal/policy"
	"github.com/PdYrust/XuiFactor/internal/store"
)

type output struct {
	w io.Writer
}

func newOutput(w io.Writer) output {
	return output{w: w}
}

func (o output) Title(text string) {
	fmt.Fprintln(o.w, text)
}

func (o output) Summary(command, message string) {
	fmt.Fprintf(o.w, "%s: %s\n", command, message)
}

func (o output) Section(title string) {
	fmt.Fprintf(o.w, "\n%s\n", title)
}

func (o output) Field(name string, value any) {
	fmt.Fprintf(o.w, "  %s: %v\n", name, value)
}

func (o output) Rule(rule store.Rule) {
	name := ""
	if rule.Name != "" {
		name = "  name=" + rule.Name
	}
	if rule.Scope != nil {
		effective := ""
		if rule.EffectiveClientCount != rule.ClientCount {
			effective = fmt.Sprintf("  effective=%d", rule.EffectiveClientCount)
		}
		fmt.Fprintf(o.w, "  rule=%d  state=%s  scope=%s  factor=%s  clients=%d%s%s\n",
			rule.ID,
			rule.State,
			scopeLabel(rule.Scope),
			engine.FormatFactor(rule.FactorPPM),
			rule.ClientCount,
			effective,
			name,
		)
		return
	}
	fmt.Fprintf(o.w, "  rule=%d  state=%s  email=%s  inbound=%d  factor=%s  clients=%d%s\n",
		rule.ID,
		rule.State,
		rule.Email,
		rule.InboundID,
		engine.FormatFactor(rule.FactorPPM),
		rule.ClientCount,
		name,
	)
}

func (o output) Exclude(policy store.ExcludePolicy) {
	note := ""
	if policy.Note != "" {
		note = "  note=" + policy.Note
	}
	fmt.Fprintf(o.w, "  exclude  email=%s  inbound=%d  traffic=%d  state=%s%s\n",
		policy.Email,
		policy.InboundID,
		policy.TrafficID,
		policy.State,
		note,
	)
}

func (o output) Override(policy store.OverridePolicy) {
	note := ""
	if policy.Note != "" {
		note = "  note=" + policy.Note
	}
	fmt.Fprintf(o.w, "  override  email=%s  inbound=%d  traffic=%d  factor=%s  state=%s%s\n",
		policy.Email,
		policy.InboundID,
		policy.TrafficID,
		engine.FormatFactor(policy.FactorPPM),
		policy.State,
		note,
	)
}

func (o output) ClientDecision(item engine.ClientEffectiveDecision) {
	fmt.Fprintf(o.w, "  email=%s  inbound=%d  traffic=%d  factor=%s  source=%s  state=%s\n",
		item.Client.Email,
		item.Client.InboundID,
		item.Client.ID,
		decisionFactor(item.Decision),
		sourceLabel(item.Decision.SourceType),
		decisionState(item.Decision),
	)
}

func (o output) Match(match policy.Match) {
	switch match.SourceType {
	case policy.SourceExclude:
		fmt.Fprintf(o.w, "  exclude  state=active\n")
	case policy.SourceUserOverride:
		fmt.Fprintf(o.w, "  override  factor=%s\n", engine.FormatFactor(match.FactorPPM))
	case policy.SourceSingleRule:
		fmt.Fprintf(o.w, "  single-user rule=%d  factor=%s\n", match.RuleID, engine.FormatFactor(match.FactorPPM))
	case policy.SourceInboundScope:
		fmt.Fprintf(o.w, "  scope  rule=%d  inbound=%d  factor=%s\n", match.RuleID, match.InboundID, engine.FormatFactor(match.FactorPPM))
	case policy.SourceGlobalScope:
		fmt.Fprintf(o.w, "  scope  rule=%d  scope=global  factor=%s\n", match.RuleID, engine.FormatFactor(match.FactorPPM))
	case policy.SourceSnapshot:
		fmt.Fprintf(o.w, "  scope  rule=%d  scope=snapshot  factor=%s\n", match.RuleID, engine.FormatFactor(match.FactorPPM))
	default:
		fmt.Fprintf(o.w, "  none\n")
	}
}

func scopeLabel(scope *store.Scope) string {
	if scope == nil {
		return "-"
	}
	mode := "global"
	if scope.InboundID != nil {
		mode = "inbound:" + strconv.FormatInt(*scope.InboundID, 10)
	}
	if scope.Once {
		mode = "snapshot," + mode
	}
	if scope.LimitedOnly {
		mode += ",limited-only"
	}
	if scope.IncludeDisabledClients {
		mode += ",include-disabled"
	}
	return mode
}

func inboundLabel(inboundID *int64) string {
	if inboundID == nil {
		return "all"
	}
	return strconv.FormatInt(*inboundID, 10)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func decisionFactor(decision policy.Decision) string {
	if decision.SourceType == policy.SourceNone || decision.SourceType == policy.SourceExclude || decision.FactorPPM == 0 {
		return "none"
	}
	return engine.FormatFactor(decision.FactorPPM)
}

func decisionMutatesTraffic(decision policy.Decision) bool {
	return decision.Target != nil && decision.FactorPPM > engine.FactorScale
}

func decisionState(decision policy.Decision) string {
	if decision.SourceType == policy.SourceNone {
		return "none"
	}
	return "active"
}

func sourceLabel(source policy.SourceType) string {
	switch source {
	case policy.SourceExclude:
		return "exclude"
	case policy.SourceUserOverride:
		return "override"
	case policy.SourceSingleRule:
		return "single-user"
	case policy.SourceInboundScope, policy.SourceGlobalScope, policy.SourceSnapshot:
		return "scope"
	default:
		return "none"
	}
}

func policyPrecedenceText() string {
	return policy.PrecedenceDescription
}

func statusRuleMatchesInbound(rule store.Rule, inboundID *int64) bool {
	if inboundID == nil || rule.Scope == nil {
		return true
	}
	if rule.Scope.InboundID == nil {
		return true
	}
	return *rule.Scope.InboundID == *inboundID
}
