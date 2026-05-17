package cli

import (
	"fmt"
	"io"
	"strconv"

	"github.com/PdYrust/XuiFactor/internal/engine"
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
