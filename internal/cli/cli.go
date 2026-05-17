package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PdYrust/XuiFactor/internal/buildinfo"
	"github.com/PdYrust/XuiFactor/internal/config"
	"github.com/PdYrust/XuiFactor/internal/daemon"
	"github.com/PdYrust/XuiFactor/internal/engine"
	"github.com/PdYrust/XuiFactor/internal/store"
)

const (
	appName     = "xui-factor"
	displayName = "XuiFactor"
)

var (
	systemctlCommand  = "systemctl"
	systemdRuntimeDir = "/run/systemd/system"
	systemdUnitFile   = "/etc/systemd/system/xui-factor.service"
)

type App struct {
	Out  io.Writer
	Err  io.Writer
	Info buildinfo.Info
}

func New(out, err io.Writer) *App {
	return &App{
		Out:  out,
		Err:  err,
		Info: buildinfo.Current(),
	}
}

func (a *App) Run(args []string) int {
	a.ensureWriters()

	common, args, err := parseCommonFlags(args)
	if err != nil {
		a.printError(err)
		return 2
	}

	if len(args) == 0 {
		a.printHelp(a.Out)
		return 0
	}

	ctx := context.Background()
	switch args[0] {
	case "-h", "--help", "help":
		a.printHelp(a.Out)
		return 0
	case "-v", "--version", "version":
		a.printVersion(a.Out)
		return 0
	case "doctor":
		return a.runDoctor(ctx, common, args[1:])
	case "status":
		return a.runStatus(ctx, common, args[1:])
	case "explain":
		return a.runExplain(ctx, common, args[1:])
	case "enable":
		return a.runEnable(ctx, common, args[1:])
	case "enable-all":
		return a.runEnableAll(ctx, common, args[1:])
	case "exclude":
		return a.runExclude(ctx, common, args[1:])
	case "unexclude":
		return a.runUnexclude(ctx, common, args[1:])
	case "excludes":
		return a.runExcludes(ctx, common, args[1:])
	case "override":
		return a.runOverride(ctx, common, args[1:])
	case "remove-override":
		return a.runRemoveOverride(ctx, common, args[1:])
	case "overrides":
		return a.runOverrides(ctx, common, args[1:])
	case "disable":
		return a.runDisable(ctx, common, args[1:])
	case "disable-all":
		return a.runDisableAll(ctx, common, args[1:])
	case "pause":
		return a.runPause(ctx, common, args[1:])
	case "pause-all":
		return a.runPauseAll(ctx, common, args[1:])
	case "resume":
		return a.runResume(ctx, common, args[1:])
	case "resume-all":
		return a.runResumeAll(ctx, common, args[1:])
	case "audit":
		return a.runAudit(ctx, common, args[1:])
	case "report":
		return a.runReport(ctx, common, args[1:])
	case "backup":
		return a.runBackup(ctx, common, args[1:])
	case "cleanup":
		return a.runCleanup(ctx, common, args[1:])
	case "reconcile":
		return a.runReconcile(ctx, common, args[1:])
	case "tick":
		return a.runTick(ctx, common, args[1:])
	case "run":
		return a.runDaemon(ctx, common, args[1:])
	default:
		fmt.Fprintf(a.Err, "%s: unknown command %q\n\n", appName, args[0])
		a.printHelp(a.Err)
		return 2
	}
}

type commonOptions struct {
	configPath   string
	databasePath string
}

func parseCommonFlags(args []string) (commonOptions, []string, error) {
	var opts commonOptions
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config":
			value, next, err := readFlagValue(args, i, "--config")
			if err != nil {
				return opts, nil, err
			}
			opts.configPath = value
			i = next
		case strings.HasPrefix(arg, "--config="):
			opts.configPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--database":
			value, next, err := readFlagValue(args, i, "--database")
			if err != nil {
				return opts, nil, err
			}
			opts.databasePath = value
			i = next
		case strings.HasPrefix(arg, "--database="):
			opts.databasePath = strings.TrimPrefix(arg, "--database=")
		default:
			rest = append(rest, arg)
		}
	}
	return opts, rest, nil
}

func (a *App) runDoctor(ctx context.Context, opts commonOptions, args []string) int {
	if len(args) != 0 {
		a.printError(fmt.Errorf("doctor: unexpected argument %q", args[0]))
		return 2
	}

	cfg, err := loadConfig(opts)
	if err != nil {
		a.printError(err)
		return 1
	}

	out := newOutput(a.Out)
	warnings := make([]string, 0, 4)
	out.Title(fmt.Sprintf("%s %s", displayName, a.Info.Version))
	out.Section("Doctor")
	out.Field("config", effectiveConfigPath(opts))
	out.Field("database", cfg.DatabasePath)
	out.Field("commit", a.Info.Commit)
	out.Field("built", a.Info.BuildTime)

	dbAccess, err := checkDatabaseFile(cfg.DatabasePath)
	if err != nil {
		a.printError(err)
		return 1
	}
	out.Section("Checks")
	out.Field("database read", "ok")
	if dbAccess.Writable {
		out.Field("database write", "ok")
	} else {
		out.Field("database write", "warning")
		warnings = append(warnings, fmt.Sprintf("database write unavailable: %v", dbAccess.WriteError))
	}

	var st *store.Store
	if dbAccess.Writable {
		st, err = store.Open(ctx, cfg.DatabasePath, cfg.BusyTimeout)
	} else {
		st, err = store.OpenReadOnly(ctx, cfg.DatabasePath, cfg.BusyTimeout)
	}
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	if err := st.ValidateRequiredSchema(ctx); err != nil {
		a.printError(err)
		return 1
	}
	out.Field("schema", "ok")
	metadataReady := false
	ready, err := st.MetadataReady(ctx)
	if err != nil {
		a.printError(err)
		return 1
	}
	metadataReady = ready
	if ready {
		out.Field("metadata", "ok")
	} else {
		out.Field("metadata", "warning")
		warnings = append(warnings, "metadata unavailable: metadata tables are missing")
	}

	if err := validateBackupDir(cfg.BackupDir); err != nil {
		a.printError(err)
		return 1
	}
	out.Field("backup dir", cfg.BackupDir)

	service := systemdServiceState()
	out.Section("Service")
	out.Field("installed", service.Installed)
	out.Field("enabled", service.Enabled)
	out.Field("active", service.Active)

	if metadataReady {
		counts, err := st.CountRules(ctx)
		if err != nil {
			a.printError(err)
			return 1
		}
		out.Section("Rules")
		out.Field("active rules", counts.Active)
		out.Field("paused rules", counts.Paused)
		out.Field("disabled rules", counts.Disabled)
		if (counts.Active > 0 || counts.Paused > 0) && (service.Active != "yes" || service.Enabled == "no") {
			warnings = append(warnings, "active rules exist but xui-factor.service is not running")
		}
		persistentScopes, err := st.CountActivePersistentScopes(ctx)
		if err != nil {
			a.printError(err)
			return 1
		}
		out.Field("persistent scopes", persistentScopes)
		if persistentScopes > 0 && service.Active != "yes" {
			warnings = append(warnings, "persistent scopes exist but future client auto-enrollment requires xui-factor.service")
		}
		excludeCounts, err := st.CountExcludes(ctx)
		if err != nil {
			a.printError(err)
			return 1
		}
		overrideCounts, err := st.CountOverrides(ctx)
		if err != nil {
			a.printError(err)
			return 1
		}
		out.Section("Policies")
		out.Field("active excludes", excludeCounts.Active)
		out.Field("inactive excludes", excludeCounts.Inactive)
		out.Field("active overrides", overrideCounts.Active)
		out.Field("inactive overrides", overrideCounts.Inactive)
	} else {
		warnings = append(warnings, "rules unavailable: metadata tables are missing")
	}
	if len(warnings) > 0 {
		out.Section("Warnings")
		for _, warning := range warnings {
			out.Field("warning", warning)
		}
	}
	fmt.Fprintln(a.Out, "doctor: OK")
	return 0
}

func (a *App) runStatus(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseStatusArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	if flags.effective {
		return a.runEffectiveStatus(ctx, opts, flags)
	}
	if flags.clients {
		return a.runClientStatus(ctx, opts, flags)
	}

	svc, st, err := a.openReadOnlyService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	metadataReady, err := st.MetadataReady(ctx)
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	if !metadataReady {
		out.Title(fmt.Sprintf("%s %s", displayName, a.Info.Version))
		out.Section("Status")
		out.Field("active scopes", 0)
		out.Field("active single-user rules", 0)
		out.Field("paused rules", 0)
		out.Field("excludes", 0)
		out.Field("overrides", 0)
		out.Field("effective factored clients", 0)
		return 0
	}

	rules, err := svc.Status(ctx, flags.includeDisabled)
	if err != nil {
		a.printError(err)
		return 1
	}
	activeScopes := 0
	activeSingles := 0
	pausedRules := 0
	for _, rule := range rules {
		if rule.State == store.StateActive {
			if rule.Scope != nil {
				activeScopes++
			} else {
				activeSingles++
			}
		}
		if rule.State == store.StatePaused {
			pausedRules++
		}
	}
	effective, err := svc.EffectiveStatus(ctx, engine.EffectiveStatusRequest{})
	if err != nil {
		a.printError(err)
		return 1
	}
	excludes, err := svc.Excludes(ctx, engine.ExcludeListRequest{IncludeInactive: flags.includeDisabled})
	if err != nil {
		a.printError(err)
		return 1
	}
	overrides, err := svc.Overrides(ctx, engine.OverrideListRequest{IncludeInactive: flags.includeDisabled})
	if err != nil {
		a.printError(err)
		return 1
	}

	out.Title(fmt.Sprintf("%s %s", displayName, a.Info.Version))
	out.Section("Status")
	out.Field("active scopes", activeScopes)
	out.Field("active single-user rules", activeSingles)
	out.Field("paused rules", pausedRules)
	out.Field("excludes", len(effective.Excludes))
	out.Field("overrides", len(effective.Overrides))
	out.Field("effective factored clients", effective.EffectiveFactoredClients)

	scopes := make([]store.Rule, 0)
	singles := make([]store.Rule, 0)
	for _, rule := range rules {
		if rule.Scope != nil {
			scopes = append(scopes, rule)
		} else {
			singles = append(singles, rule)
		}
	}
	if len(scopes) > 0 {
		out.Section("Scopes")
		for _, rule := range scopes {
			out.Rule(rule)
		}
	}
	if len(singles) > 0 {
		out.Section("Rules")
		for _, rule := range singles {
			out.Rule(rule)
		}
	}
	if len(excludes) > 0 || len(overrides) > 0 {
		out.Section("Policies")
		for _, policy := range excludes {
			out.Exclude(policy)
		}
		for _, policy := range overrides {
			out.Override(policy)
		}
	}
	service := systemdServiceState()
	out.Section("Service")
	out.Field("enabled", service.Enabled)
	out.Field("active", service.Active)
	if flags.includeDisabled && len(rules) == 0 {
		out.Section("Rules")
		out.Field("entries", 0)
	}
	return 0
}

func (a *App) runEffectiveStatus(ctx context.Context, opts commonOptions, flags statusFlags) int {
	svc, st, err := a.openReadOnlyService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	result, err := svc.EffectiveStatus(ctx, engine.EffectiveStatusRequest{InboundID: flags.inboundID})
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Title(fmt.Sprintf("%s %s", displayName, a.Info.Version))
	out.Section("Effective Status")
	out.Field("scopes", result.Scopes)
	out.Field("excludes", len(result.Excludes))
	out.Field("overrides", len(result.Overrides))
	out.Field("effective factored clients", result.EffectiveFactoredClients)
	out.Field("excluded clients", result.ExcludedClients)
	out.Field("overridden clients", result.OverriddenClients)

	scopes := make([]store.Rule, 0)
	for _, rule := range result.Rules {
		if rule.Scope != nil && rule.State == store.StateActive && statusRuleMatchesInbound(rule, flags.inboundID) {
			scopes = append(scopes, rule)
		}
	}
	if len(scopes) > 0 {
		out.Section("Scopes")
		for _, rule := range scopes {
			out.Rule(rule)
		}
	}
	if len(result.Excludes) > 0 || len(result.Overrides) > 0 {
		out.Section("Policies")
		for _, policy := range result.Excludes {
			out.Exclude(policy)
		}
		for _, policy := range result.Overrides {
			out.Override(policy)
		}
	}
	return 0
}

func (a *App) runClientStatus(ctx context.Context, opts commonOptions, flags statusFlags) int {
	svc, st, err := a.openReadOnlyService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	limit := 0
	if flags.inboundID == nil {
		limit = engine.DefaultClientStatusLimit
	}
	result, err := svc.ClientStatus(ctx, engine.ClientStatusRequest{InboundID: flags.inboundID, Limit: limit})
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Title("Clients")
	if len(result.Clients) == 0 {
		out.Field("entries", 0)
		return 0
	}
	for _, item := range result.Clients {
		out.ClientDecision(item)
	}
	if result.Truncated {
		out.Section("Hint")
		out.Field("showing", result.Limit)
		out.Field("run", "xui-factor status --clients --inbound-id ID")
	}
	return 0
}

func (a *App) runExplain(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseExplainArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, err := a.openReadOnlyService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	result, err := svc.Explain(ctx, engine.ExplainRequest{Email: flags.email, InboundID: flags.inboundID})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.printTargetError("client not found", flags.email, flags.inboundID, "xui-factor status --clients --inbound-id ID")
			return commandErrorCode(err)
		}
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	out.Summary("explain", "effective decision")
	out.Section("Target")
	out.Field("email", result.Client.Email)
	out.Field("inbound", result.Client.InboundID)
	out.Field("traffic_id", result.Client.ID)
	out.Section("Decision")
	out.Field("effective factor", decisionFactor(result.Decision))
	out.Field("source", sourceLabel(result.Decision.SourceType))
	out.Field("mutates traffic", yesNo(decisionMutatesTraffic(result.Decision)))
	if result.Baseline != nil {
		out.Section("Baseline")
		out.Field("last up", result.Baseline.LastUp)
		out.Field("last down", result.Baseline.LastDown)
		out.Field("last all time", result.Baseline.LastAllTime)
	}
	if len(result.Decision.Matched) > 0 {
		out.Section("Matched")
		for _, match := range result.Decision.Matched {
			out.Match(match)
		}
	}
	out.Section("Precedence")
	out.Field("order", policyPrecedenceText())
	out.Section("Result")
	out.Field("status", engine.ExplainResultText(result))
	return 0
}

func (a *App) runEnable(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseEnableArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	rule, err := svc.Enable(ctx, engine.EnableRequest{
		Name:      flags.name,
		Email:     flags.email,
		InboundID: flags.inboundID,
		Factor:    flags.factor,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.printTargetError("client not found", flags.email, flags.inboundID, "xui-factor status")
			return commandErrorCode(err)
		}
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	out.Summary("enable", "rule active")
	out.Section("Rule")
	out.Field("rule", rule.ID)
	out.Field("email", rule.Email)
	out.Field("inbound", rule.InboundID)
	out.Field("factor", engine.FormatFactor(rule.FactorPPM))
	return 0
}

func (a *App) runEnableAll(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseEnableAllArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	result, err := svc.EnableAll(ctx, engine.EnableAllRequest{
		Factor:                 flags.factor,
		InboundID:              flags.inboundID,
		LimitedOnly:            flags.limitedOnly,
		IncludeDisabledClients: flags.includeDisabledClients,
		Once:                   flags.once,
		Name:                   flags.name,
	})
	if err != nil {
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	if result.Mode == "snapshot" {
		out.Summary("enable-all", "snapshot scope updated")
	} else {
		out.Summary("enable-all", "persistent scope updated")
	}
	out.Section("Scope")
	out.Field("inbound", inboundLabel(flags.inboundID))
	out.Field("factor", flags.factor)
	out.Field("mode", result.Mode)
	if flags.limitedOnly {
		out.Field("limited only", yesNo(flags.limitedOnly))
	}
	if flags.includeDisabledClients {
		out.Field("include disabled clients", yesNo(flags.includeDisabledClients))
	}
	out.Section("Result")
	out.Field("matched", result.Matched)
	out.Field("enrolled", result.Changed)
	out.Field("adopted", result.Adopted)
	out.Field("skipped", result.SkippedExisting)
	out.Field("conflicts", result.Conflicts)
	out.Field("missing", result.Missing)
	return 0
}

func (a *App) runExclude(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseExcludeArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	policy, err := svc.Exclude(ctx, engine.ExcludeRequest{
		Email:     flags.email,
		InboundID: flags.inboundID,
		Note:      flags.note,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.printTargetError("client not found", flags.email, flags.inboundID, "xui-factor status")
			return commandErrorCode(err)
		}
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	out.Summary("exclude", "policy enabled")
	out.Section("Target")
	out.Field("email", policy.Email)
	out.Field("inbound", policy.InboundID)
	out.Field("traffic", policy.TrafficID)
	out.Section("Policy")
	out.Field("action", "no factor")
	out.Field("precedence", "exclude")
	out.Section("Result")
	out.Field("status", policy.State)
	return 0
}

func (a *App) runUnexclude(ctx context.Context, opts commonOptions, args []string) int {
	selector, err := parseExcludeSelectorArgs(args, "unexclude")
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	policy, err := svc.Unexclude(ctx, engine.ExcludeSelector{
		Email:     selector.email,
		InboundID: selector.inboundID,
	})
	if err != nil {
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	out.Summary("unexclude", "policy disabled")
	out.Section("Target")
	out.Field("email", policy.Email)
	out.Field("inbound", policy.InboundID)
	out.Field("traffic", policy.TrafficID)
	out.Section("Result")
	out.Field("future traffic", "follows matching rules and scopes")
	return 0
}

func (a *App) runExcludes(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseExcludesArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, err := a.openReadOnlyService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	policies, err := svc.Excludes(ctx, engine.ExcludeListRequest{
		InboundID:       flags.inboundID,
		IncludeInactive: flags.includeInactive,
	})
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Title("Excludes")
	if len(policies) == 0 {
		out.Field("entries", 0)
		return 0
	}
	for _, policy := range policies {
		out.Exclude(policy)
	}
	return 0
}

func (a *App) runOverride(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseOverrideArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	policy, err := svc.Override(ctx, engine.OverrideRequest{
		Email:     flags.email,
		InboundID: flags.inboundID,
		Factor:    flags.factor,
		Note:      flags.note,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.printTargetError("client not found", flags.email, flags.inboundID, "xui-factor status")
			return commandErrorCode(err)
		}
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	out.Summary("override", "policy enabled")
	out.Section("Target")
	out.Field("email", policy.Email)
	out.Field("inbound", policy.InboundID)
	out.Field("traffic", policy.TrafficID)
	out.Section("Policy")
	out.Field("factor", engine.FormatFactor(policy.FactorPPM))
	out.Field("precedence", "user override")
	out.Section("Result")
	out.Field("status", policy.State)
	out.Field("note", "future traffic uses this factor while the override is active")
	return 0
}

func (a *App) runRemoveOverride(ctx context.Context, opts commonOptions, args []string) int {
	selector, err := parseOverrideSelectorArgs(args, "remove-override")
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	policy, err := svc.RemoveOverride(ctx, engine.OverrideSelector{
		Email:     selector.email,
		InboundID: selector.inboundID,
	})
	if err != nil {
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	out.Summary("remove-override", "policy disabled")
	out.Section("Target")
	out.Field("email", policy.Email)
	out.Field("inbound", policy.InboundID)
	out.Field("traffic", policy.TrafficID)
	out.Section("Result")
	out.Field("future traffic", "follows matching rules and scopes")
	return 0
}

func (a *App) runOverrides(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseOverridesArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, err := a.openReadOnlyService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	policies, err := svc.Overrides(ctx, engine.OverrideListRequest{
		InboundID:       flags.inboundID,
		IncludeInactive: flags.includeInactive,
	})
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Title("Overrides")
	if len(policies) == 0 {
		out.Field("entries", 0)
		return 0
	}
	for _, policy := range policies {
		out.Override(policy)
	}
	return 0
}

func (a *App) runDisable(ctx context.Context, opts commonOptions, args []string) int {
	return a.runLifecycle(ctx, opts, args, "disable", "disabled", func(svc *engine.Service, ctx context.Context, selector engine.RuleSelector) (store.Rule, error) {
		return svc.Disable(ctx, selector)
	})
}

func (a *App) runDisableAll(ctx context.Context, opts commonOptions, args []string) int {
	return a.runBulkLifecycle(ctx, opts, args, "disable-all", func(svc *engine.Service, ctx context.Context, selector engine.BulkSelector) (engine.BulkResult, error) {
		return svc.DisableAll(ctx, selector)
	})
}

func (a *App) runPause(ctx context.Context, opts commonOptions, args []string) int {
	return a.runLifecycle(ctx, opts, args, "pause", "paused", func(svc *engine.Service, ctx context.Context, selector engine.RuleSelector) (store.Rule, error) {
		return svc.Pause(ctx, selector)
	})
}

func (a *App) runPauseAll(ctx context.Context, opts commonOptions, args []string) int {
	return a.runBulkLifecycle(ctx, opts, args, "pause-all", func(svc *engine.Service, ctx context.Context, selector engine.BulkSelector) (engine.BulkResult, error) {
		return svc.PauseAll(ctx, selector)
	})
}

func (a *App) runResume(ctx context.Context, opts commonOptions, args []string) int {
	return a.runLifecycle(ctx, opts, args, "resume", "active", func(svc *engine.Service, ctx context.Context, selector engine.RuleSelector) (store.Rule, error) {
		return svc.Resume(ctx, selector)
	})
}

func (a *App) runResumeAll(ctx context.Context, opts commonOptions, args []string) int {
	return a.runBulkLifecycle(ctx, opts, args, "resume-all", func(svc *engine.Service, ctx context.Context, selector engine.BulkSelector) (engine.BulkResult, error) {
		return svc.ResumeAll(ctx, selector)
	})
}

func (a *App) runLifecycle(ctx context.Context, opts commonOptions, args []string, command, resultState string, fn func(*engine.Service, context.Context, engine.RuleSelector) (store.Rule, error)) int {
	selector, err := parseSelectorArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	rule, err := fn(svc, ctx, selector)
	if err != nil {
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	out.Summary(command, "rule "+resultState)
	out.Section("Rule")
	out.Field("rule", rule.ID)
	out.Field("email", rule.Email)
	out.Field("inbound", rule.InboundID)
	return 0
}

func (a *App) runBulkLifecycle(ctx context.Context, opts commonOptions, args []string, label string, fn func(*engine.Service, context.Context, engine.BulkSelector) (engine.BulkResult, error)) int {
	selector, err := parseBulkSelectorArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	result, err := fn(svc, ctx, selector)
	if err != nil {
		a.printError(err)
		return commandErrorCode(err)
	}
	out := newOutput(a.Out)
	out.Summary(label, "completed")
	out.Section("Result")
	out.Field("matched", result.Matched)
	out.Field("changed", result.Changed)
	out.Field("adopted", result.Adopted)
	out.Field("skipped", result.SkippedExisting)
	out.Field("conflicts", result.Conflicts)
	out.Field("missing", result.Missing)
	return 0
}

func (a *App) runReport(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseReportArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	cfg, err := loadConfig(opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	st, err := store.OpenReadOnly(ctx, cfg.DatabasePath, cfg.BusyTimeout)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()
	if err := st.ValidateRequiredSchema(ctx); err != nil {
		a.printError(err)
		return 1
	}
	svc := engine.New(st)
	result, err := svc.Report(ctx, engine.ReportRequest{InboundID: flags.inboundID, IncludeAll: flags.includeAll})
	if err != nil {
		a.printError(err)
		return 1
	}

	service := systemdServiceState()
	out := newOutput(a.Out)
	out.Title(fmt.Sprintf("%s %s", displayName, a.Info.Version))
	out.Section("Report")
	out.Field("database", cfg.DatabasePath)
	if flags.inboundID != nil {
		out.Field("inbound", *flags.inboundID)
	}
	out.Field("service", service.Active)
	if result.MetadataReady {
		out.Field("metadata", "ok")
	} else {
		out.Field("metadata", "unavailable")
		out.Section("Policies")
		out.Field("active scopes", 0)
		out.Field("active excludes", 0)
		out.Field("active overrides", 0)
		out.Field("active single-user rules", 0)
		return 0
	}

	out.Section("Policies")
	out.Field("active scopes", result.ActiveScopes)
	out.Field("active excludes", result.ActiveExcludes)
	out.Field("active overrides", result.ActiveOverrides)
	out.Field("active single-user rules", result.ActiveSingleUserRules)
	if flags.includeAll {
		out.Field("paused rules", result.PausedRules)
		out.Field("inactive rules", result.DisabledRules)
		out.Field("inactive excludes", result.InactiveExcludes)
		out.Field("inactive overrides", result.InactiveOverrides)
	}

	out.Section("Effective Clients")
	out.Field("factored", result.EffectiveFactoredClients)
	out.Field("excluded", result.ExcludedClients)
	out.Field("overridden", result.OverriddenClients)
	out.Field("no factor", result.NoFactorClients)
	out.Field("active rule clients", result.TotalActiveRuleClients)

	out.Section("Traffic Impact")
	out.Field("extra applied", formatBytes(result.TrafficImpact.ExtraBytes))
	out.Field("tick applications", result.TrafficImpact.Applications)
	if result.TrafficImpact.LastAppliedAt != nil {
		out.Field("last tick", formatEventTime(*result.TrafficImpact.LastAppliedAt))
	} else {
		out.Field("last tick", "unavailable")
	}
	return 0
}

func (a *App) runAudit(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseAuditArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}

	svc, st, err := a.openReadOnlyService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	metadataReady, err := st.MetadataReady(ctx)
	if err != nil {
		a.printError(err)
		return 1
	}
	if !metadataReady {
		out := newOutput(a.Out)
		out.Title("Audit")
		fmt.Fprintln(a.Out, "  no events matched")
		return 0
	}

	events, err := svc.Audit(ctx, engine.AuditRequest{
		Limit:     flags.limit,
		EventType: flags.eventType,
		Email:     flags.email,
		InboundID: flags.inboundID,
		RuleID:    flags.ruleID,
		PolicyID:  flags.policyID,
		Since:     flags.since,
	})
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Title("Audit")
	out.Field("filter", auditFilterSummary(flags))
	if len(events) == 0 {
		fmt.Fprintln(a.Out, "  no events matched")
		return 0
	}
	out.Section("Events")
	for _, event := range events {
		out.Event(event)
	}
	return 0
}

func auditFilterSummary(flags auditFlags) string {
	parts := make([]string, 0, 7)
	if flags.eventType != "" {
		parts = append(parts, "event="+flags.eventType)
	}
	if flags.email != "" {
		parts = append(parts, "email="+flags.email)
	}
	if flags.inboundID != nil {
		parts = append(parts, "inbound="+strconv.FormatInt(*flags.inboundID, 10))
	}
	if flags.ruleID != nil {
		parts = append(parts, "rule="+strconv.FormatInt(*flags.ruleID, 10))
	}
	if flags.policyID != nil {
		parts = append(parts, "policy="+strconv.FormatInt(*flags.policyID, 10))
	}
	if flags.sinceText != "" {
		parts = append(parts, "since="+flags.sinceText)
	}
	parts = append(parts, "limit="+strconv.Itoa(flags.limit))
	return strings.Join(parts, " ")
}

func (a *App) runBackup(ctx context.Context, opts commonOptions, args []string) int {
	if len(args) != 0 {
		a.printError(fmt.Errorf("backup: unexpected argument %q", args[0]))
		return 2
	}
	_, st, cfg, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	path, err := st.Backup(ctx, cfg.BackupDir, time.Now())
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Summary("backup", "created")
	out.Section("Backup")
	out.Field("path", path)
	out.Section("Restore")
	out.Field("mode", "manual only")
	out.Field("hint", "stop x-ui and xui-factor before replacing the database")
	return 0
}

func (a *App) runCleanup(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseCleanupArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, cfg, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	result, err := svc.Cleanup(ctx, engine.CleanupRequest{
		Config:    cfg,
		DryRun:    flags.dryRun,
		OlderThan: flags.olderThan,
		Vacuum:    flags.vacuum,
	})
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Summary("cleanup", "completed")
	out.Section("Result")
	out.Field("missing clients pruned", result.MissingClientsPruned)
	out.Field("disabled rules pruned", result.DisabledRulesPruned)
	out.Field("disabled scopes pruned", result.DisabledScopesPruned)
	out.Field("inactive excludes pruned", result.InactiveExcludesPruned)
	out.Field("inactive overrides pruned", result.InactiveOverridesPruned)
	out.Field("audit events pruned", result.AuditEventsPruned)
	out.Field("vacuum run", yesNo(result.VacuumRun))
	return 0
}

func (a *App) runReconcile(ctx context.Context, opts commonOptions, args []string) int {
	flags, err := parseReconcileArgs(args)
	if err != nil {
		a.printError(err)
		return 2
	}
	svc, st, _, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	result, err := svc.Reconcile(ctx, engine.ReconcileRequest{
		InboundID: flags.inboundID,
		DryRun:    flags.dryRun,
	})
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Summary("reconcile", "completed")
	out.Section("Result")
	out.Field("checked", result.Checked)
	out.Field("reconciled", result.Reconciled)
	out.Field("orphaned", result.Orphaned)
	out.Field("disabled clients", result.DisabledClients)
	out.Field("superseded", result.Superseded)
	out.Field("conflicts", result.Conflicts)
	return 0
}

func (a *App) runTick(ctx context.Context, opts commonOptions, args []string) int {
	if len(args) != 0 {
		a.printError(fmt.Errorf("tick: unexpected argument %q", args[0]))
		return 2
	}
	_, st, cfg, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	runner := daemon.New(st, cfg, a.Out, a.Err)
	result, err := runner.Tick(ctx)
	if err != nil {
		a.printError(err)
		return 1
	}
	out := newOutput(a.Out)
	out.Summary("tick", "completed")
	out.Section("Result")
	out.Field("active clients", result.ActiveClients)
	out.Field("reconciled", result.Reconciled)
	out.Field("enrolled", result.Enrolled)
	out.Field("enroll skipped", result.EnrollSkipped)
	out.Field("applied", result.Applied)
	out.Field("baselined", result.Baselined)
	out.Field("rebaselined", result.Rebaselined)
	out.Field("missing", result.Missing)
	return 0
}

func (a *App) runDaemon(ctx context.Context, opts commonOptions, args []string) int {
	if len(args) != 0 {
		a.printError(fmt.Errorf("run: unexpected argument %q", args[0]))
		return 2
	}
	_, st, cfg, err := a.openService(ctx, opts)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer st.Close()

	lock, err := daemon.AcquireLock(daemon.DefaultLockPath)
	if err != nil {
		a.printError(err)
		return 1
	}
	defer lock.Close()

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := daemon.New(st, cfg, a.Out, a.Err)
	if err := runner.Run(runCtx); err != nil {
		a.printError(err)
		return 1
	}
	return 0
}

func loadConfig(opts commonOptions) (config.Config, error) {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return config.Config{}, err
	}
	if strings.TrimSpace(opts.databasePath) != "" {
		cfg.DatabasePath = strings.TrimSpace(opts.databasePath)
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func effectiveConfigPath(opts commonOptions) string {
	if strings.TrimSpace(opts.configPath) != "" {
		return strings.TrimSpace(opts.configPath)
	}
	return config.DefaultPath
}

type databaseFileAccess struct {
	Writable   bool
	WriteError error
}

func checkDatabaseFile(path string) (databaseFileAccess, error) {
	info, err := os.Stat(path)
	if err != nil {
		return databaseFileAccess{}, fmt.Errorf("database file: %w", err)
	}
	if info.IsDir() {
		return databaseFileAccess{}, fmt.Errorf("database file: %s is a directory", path)
	}
	readFile, err := os.Open(path)
	if err != nil {
		return databaseFileAccess{}, fmt.Errorf("database file is not readable: %w", err)
	}
	if err := readFile.Close(); err != nil {
		return databaseFileAccess{}, fmt.Errorf("database file read check: %w", err)
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return databaseFileAccess{Writable: false, WriteError: err}, nil
	}
	if err := file.Close(); err != nil {
		return databaseFileAccess{}, fmt.Errorf("database file write check: %w", err)
	}
	return databaseFileAccess{Writable: true}, nil
}

func requireWritableDatabaseFile(path string) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	access, err := checkDatabaseFile(path)
	if err != nil {
		return err
	}
	if !access.Writable {
		return fmt.Errorf("database file is not writable; this command requires write access: %w", access.WriteError)
	}
	return nil
}

func validateBackupDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("backup_dir is required")
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return fmt.Errorf("backup_dir: %w", err)
	}
	tmp, err := os.CreateTemp(path, ".doctor-*.tmp")
	if err != nil {
		return fmt.Errorf("backup_dir is not writable: %w", err)
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("backup_dir check: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("backup_dir check: %w", err)
	}
	return nil
}

type serviceState struct {
	Installed string
	Enabled   string
	Active    string
}

func systemdServiceState() serviceState {
	state := serviceState{
		Installed: serviceInstalledFromUnitFile(),
		Enabled:   "unknown",
		Active:    "unknown",
	}
	if _, err := exec.LookPath(systemctlCommand); err != nil {
		return state
	}
	if _, err := os.Stat(systemdRuntimeDir); err != nil {
		return state
	}

	state.Enabled = serviceEnabledState()
	state.Active = serviceActiveState()
	if state.Installed != "yes" && (state.Enabled == "yes" || state.Active == "yes") {
		state.Installed = "yes"
	}
	return state
}

func serviceInstalledFromUnitFile() string {
	if _, err := os.Stat(systemdUnitFile); err == nil {
		return "yes"
	} else if errors.Is(err, os.ErrNotExist) {
		return "no"
	}
	return "unknown"
}

func serviceEnabledState() string {
	output, err := exec.Command(systemctlCommand, "is-enabled", "xui-factor.service").CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err == nil {
		return "yes"
	}
	switch text {
	case "disabled", "indirect", "static", "masked":
		return "no"
	case "":
		return "unknown"
	default:
		if strings.Contains(text, "not-found") || strings.Contains(text, "not found") {
			return "no"
		}
		return "unknown"
	}
}

func serviceActiveState() string {
	output, err := exec.Command(systemctlCommand, "is-active", "xui-factor.service").CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err == nil {
		return "yes"
	}
	switch text {
	case "inactive", "failed", "deactivating", "activating", "reloading":
		return "no"
	case "":
		return "unknown"
	default:
		if strings.Contains(text, "not-found") || strings.Contains(text, "not found") {
			return "no"
		}
		return "unknown"
	}
}

func (a *App) openService(ctx context.Context, opts commonOptions) (*engine.Service, *store.Store, config.Config, error) {
	cfg, err := loadConfig(opts)
	if err != nil {
		return nil, nil, config.Config{}, err
	}
	if err := requireWritableDatabaseFile(cfg.DatabasePath); err != nil {
		return nil, nil, config.Config{}, err
	}
	st, err := store.Open(ctx, cfg.DatabasePath, cfg.BusyTimeout)
	if err != nil {
		return nil, nil, config.Config{}, err
	}
	if err := st.EnsureReady(ctx); err != nil {
		st.Close()
		return nil, nil, config.Config{}, err
	}
	return engine.New(st), st, cfg, nil
}

func (a *App) openReadOnlyService(ctx context.Context, opts commonOptions) (*engine.Service, *store.Store, error) {
	cfg, err := loadConfig(opts)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.OpenReadOnly(ctx, cfg.DatabasePath, cfg.BusyTimeout)
	if err != nil {
		return nil, nil, err
	}
	if err := st.ValidateRequiredSchema(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}
	return engine.New(st), st, nil
}

type enableFlags struct {
	email     string
	inboundID *int64
	factor    string
	name      string
}

type enableAllFlags struct {
	factor                 string
	inboundID              *int64
	limitedOnly            bool
	includeDisabledClients bool
	once                   bool
	name                   string
}

type excludeFlags struct {
	email     string
	inboundID *int64
	note      string
}

type excludesFlags struct {
	inboundID       *int64
	includeInactive bool
}

type overrideFlags struct {
	email     string
	inboundID *int64
	factor    string
	note      string
}

type overridesFlags struct {
	inboundID       *int64
	includeInactive bool
}

type statusFlags struct {
	includeDisabled bool
	effective       bool
	clients         bool
	inboundID       *int64
}

type explainFlags struct {
	email     string
	inboundID *int64
}

type reportFlags struct {
	inboundID  *int64
	includeAll bool
}

type auditFlags struct {
	limit     int
	eventType string
	email     string
	inboundID *int64
	ruleID    *int64
	policyID  *int64
	since     time.Duration
	sinceText string
}

type cleanupFlags struct {
	dryRun    bool
	olderThan time.Duration
	vacuum    bool
}

type reconcileFlags struct {
	dryRun    bool
	inboundID *int64
}

func parseEnableArgs(args []string) (enableFlags, error) {
	var flags enableFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--email":
			value, next, err := readFlagValue(args, i, "--email")
			if err != nil {
				return flags, err
			}
			flags.email = value
			i = next
		case strings.HasPrefix(arg, "--email="):
			flags.email = strings.TrimPrefix(arg, "--email=")
		case arg == "--factor":
			value, next, err := readFlagValue(args, i, "--factor")
			if err != nil {
				return flags, err
			}
			flags.factor = value
			i = next
		case strings.HasPrefix(arg, "--factor="):
			flags.factor = strings.TrimPrefix(arg, "--factor=")
		case arg == "--name":
			value, next, err := readFlagValue(args, i, "--name")
			if err != nil {
				return flags, err
			}
			flags.name = value
			i = next
		case strings.HasPrefix(arg, "--name="):
			flags.name = strings.TrimPrefix(arg, "--name=")
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		default:
			return flags, fmt.Errorf("enable: unknown argument %q", arg)
		}
	}
	if strings.TrimSpace(flags.email) == "" {
		return flags, errors.New("enable: --email is required")
	}
	if strings.TrimSpace(flags.factor) == "" {
		return flags, errors.New("enable: --factor is required")
	}
	return flags, nil
}

func parseEnableAllArgs(args []string) (enableAllFlags, error) {
	var flags enableAllFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--factor":
			value, next, err := readFlagValue(args, i, "--factor")
			if err != nil {
				return flags, err
			}
			flags.factor = value
			i = next
		case strings.HasPrefix(arg, "--factor="):
			flags.factor = strings.TrimPrefix(arg, "--factor=")
		case arg == "--name":
			value, next, err := readFlagValue(args, i, "--name")
			if err != nil {
				return flags, err
			}
			flags.name = value
			i = next
		case strings.HasPrefix(arg, "--name="):
			flags.name = strings.TrimPrefix(arg, "--name=")
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		case arg == "--limited-only":
			flags.limitedOnly = true
		case arg == "--include-disabled-clients":
			flags.includeDisabledClients = true
		case arg == "--once":
			flags.once = true
		default:
			return flags, fmt.Errorf("enable-all: unknown argument %q", arg)
		}
	}
	if strings.TrimSpace(flags.factor) == "" {
		return flags, errors.New("enable-all: --factor is required")
	}
	return flags, nil
}

func parseExcludeArgs(args []string) (excludeFlags, error) {
	flags, err := parseExcludeSelectorArgs(args, "exclude")
	if err != nil {
		return flags, err
	}
	return flags, nil
}

func parseExcludeSelectorArgs(args []string, command string) (excludeFlags, error) {
	var flags excludeFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--email":
			value, next, err := readFlagValue(args, i, "--email")
			if err != nil {
				return flags, err
			}
			flags.email = value
			i = next
		case strings.HasPrefix(arg, "--email="):
			flags.email = strings.TrimPrefix(arg, "--email=")
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		case arg == "--note" && command == "exclude":
			value, next, err := readFlagValue(args, i, "--note")
			if err != nil {
				return flags, err
			}
			flags.note = value
			i = next
		case strings.HasPrefix(arg, "--note=") && command == "exclude":
			flags.note = strings.TrimPrefix(arg, "--note=")
		default:
			return flags, fmt.Errorf("%s: unknown argument %q", command, arg)
		}
	}
	if strings.TrimSpace(flags.email) == "" {
		return flags, fmt.Errorf("%s: --email is required", command)
	}
	if flags.inboundID == nil {
		return flags, fmt.Errorf("%s: --inbound-id is required", command)
	}
	return flags, nil
}

func parseExcludesArgs(args []string) (excludesFlags, error) {
	var flags excludesFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			flags.includeInactive = true
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		default:
			return flags, fmt.Errorf("excludes: unknown argument %q", arg)
		}
	}
	return flags, nil
}

func parseOverrideArgs(args []string) (overrideFlags, error) {
	flags, err := parseOverrideSelectorArgs(args, "override")
	if err != nil {
		return flags, err
	}
	if strings.TrimSpace(flags.factor) == "" {
		return flags, errors.New("override: --factor is required")
	}
	return flags, nil
}

func parseOverrideSelectorArgs(args []string, command string) (overrideFlags, error) {
	var flags overrideFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--email":
			value, next, err := readFlagValue(args, i, "--email")
			if err != nil {
				return flags, err
			}
			flags.email = value
			i = next
		case strings.HasPrefix(arg, "--email="):
			flags.email = strings.TrimPrefix(arg, "--email=")
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		case arg == "--factor" && command == "override":
			value, next, err := readFlagValue(args, i, "--factor")
			if err != nil {
				return flags, err
			}
			flags.factor = value
			i = next
		case strings.HasPrefix(arg, "--factor=") && command == "override":
			flags.factor = strings.TrimPrefix(arg, "--factor=")
		case arg == "--note" && command == "override":
			value, next, err := readFlagValue(args, i, "--note")
			if err != nil {
				return flags, err
			}
			flags.note = value
			i = next
		case strings.HasPrefix(arg, "--note=") && command == "override":
			flags.note = strings.TrimPrefix(arg, "--note=")
		default:
			return flags, fmt.Errorf("%s: unknown argument %q", command, arg)
		}
	}
	if strings.TrimSpace(flags.email) == "" {
		return flags, fmt.Errorf("%s: --email is required", command)
	}
	if flags.inboundID == nil {
		return flags, fmt.Errorf("%s: --inbound-id is required", command)
	}
	return flags, nil
}

func parseOverridesArgs(args []string) (overridesFlags, error) {
	var flags overridesFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			flags.includeInactive = true
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		default:
			return flags, fmt.Errorf("overrides: unknown argument %q", arg)
		}
	}
	return flags, nil
}

func parseStatusArgs(args []string) (statusFlags, error) {
	var flags statusFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--include-disabled", arg == "--all":
			flags.includeDisabled = true
		case arg == "--effective":
			flags.effective = true
		case arg == "--clients":
			flags.clients = true
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		default:
			return flags, fmt.Errorf("status: unknown argument %q", arg)
		}
	}
	if flags.effective && flags.clients {
		return flags, errors.New("status: --effective and --clients cannot be used together")
	}
	if flags.inboundID != nil && !flags.effective && !flags.clients {
		return flags, errors.New("status: --inbound-id requires --effective or --clients")
	}
	return flags, nil
}

func parseExplainArgs(args []string) (explainFlags, error) {
	var flags explainFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--email":
			value, next, err := readFlagValue(args, i, "--email")
			if err != nil {
				return flags, err
			}
			flags.email = value
			i = next
		case strings.HasPrefix(arg, "--email="):
			flags.email = strings.TrimPrefix(arg, "--email=")
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		default:
			return flags, fmt.Errorf("explain: unknown argument %q", arg)
		}
	}
	if strings.TrimSpace(flags.email) == "" {
		return flags, errors.New("explain: --email is required")
	}
	if flags.inboundID == nil {
		return flags, errors.New("explain: --inbound-id is required")
	}
	return flags, nil
}

func parseReportArgs(args []string) (reportFlags, error) {
	var flags reportFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			flags.includeAll = true
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		default:
			return flags, fmt.Errorf("report: unknown argument %q", arg)
		}
	}
	return flags, nil
}

func parseAuditArgs(args []string) (auditFlags, error) {
	flags := auditFlags{limit: 50}
	eventSpecified := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--limit":
			value, next, err := readFlagValue(args, i, "--limit")
			if err != nil {
				return flags, err
			}
			limit, err := parsePositiveInt(value, "--limit")
			if err != nil {
				return flags, err
			}
			flags.limit = limit
			i = next
		case strings.HasPrefix(arg, "--limit="):
			limit, err := parsePositiveInt(strings.TrimPrefix(arg, "--limit="), "--limit")
			if err != nil {
				return flags, err
			}
			flags.limit = limit
		case arg == "--event":
			value, next, err := readFlagValue(args, i, "--event")
			if err != nil {
				return flags, err
			}
			eventSpecified = true
			flags.eventType = normalizeAuditEventType(value)
			i = next
		case strings.HasPrefix(arg, "--event="):
			eventSpecified = true
			flags.eventType = normalizeAuditEventType(strings.TrimPrefix(arg, "--event="))
		case arg == "--email":
			value, next, err := readFlagValue(args, i, "--email")
			if err != nil {
				return flags, err
			}
			flags.email = value
			i = next
		case strings.HasPrefix(arg, "--email="):
			flags.email = strings.TrimPrefix(arg, "--email=")
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		case arg == "--rule-id":
			value, next, err := readFlagValue(args, i, "--rule-id")
			if err != nil {
				return flags, err
			}
			ruleID, err := parsePositiveInt64(value, "--rule-id")
			if err != nil {
				return flags, err
			}
			flags.ruleID = &ruleID
			i = next
		case strings.HasPrefix(arg, "--rule-id="):
			ruleID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--rule-id="), "--rule-id")
			if err != nil {
				return flags, err
			}
			flags.ruleID = &ruleID
		case arg == "--policy-id":
			value, next, err := readFlagValue(args, i, "--policy-id")
			if err != nil {
				return flags, err
			}
			policyID, err := parsePositiveInt64(value, "--policy-id")
			if err != nil {
				return flags, err
			}
			flags.policyID = &policyID
			i = next
		case strings.HasPrefix(arg, "--policy-id="):
			policyID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--policy-id="), "--policy-id")
			if err != nil {
				return flags, err
			}
			flags.policyID = &policyID
		case arg == "--since":
			value, next, err := readFlagValue(args, i, "--since")
			if err != nil {
				return flags, err
			}
			since, err := parseDurationFlag(value, "audit: --since")
			if err != nil {
				return flags, err
			}
			flags.since = since
			flags.sinceText = value
			i = next
		case strings.HasPrefix(arg, "--since="):
			value := strings.TrimPrefix(arg, "--since=")
			since, err := parseDurationFlag(value, "audit: --since")
			if err != nil {
				return flags, err
			}
			flags.since = since
			flags.sinceText = value
		default:
			return flags, fmt.Errorf("audit: unknown argument %q", arg)
		}
	}
	if eventSpecified && flags.eventType == "" {
		return flags, errors.New("audit: --event is required")
	}
	return flags, nil
}

func normalizeAuditEventType(value string) string {
	eventType := strings.TrimSpace(value)
	if eventType == "tick" {
		return store.EventTrafficApply
	}
	return eventType
}

func parseCleanupArgs(args []string) (cleanupFlags, error) {
	var flags cleanupFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dry-run":
			flags.dryRun = true
		case arg == "--vacuum":
			flags.vacuum = true
		case arg == "--older-than":
			value, next, err := readFlagValue(args, i, "--older-than")
			if err != nil {
				return flags, err
			}
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return flags, errors.New("cleanup: --older-than must be a positive duration")
			}
			flags.olderThan = d
			i = next
		case strings.HasPrefix(arg, "--older-than="):
			d, err := time.ParseDuration(strings.TrimPrefix(arg, "--older-than="))
			if err != nil || d <= 0 {
				return flags, errors.New("cleanup: --older-than must be a positive duration")
			}
			flags.olderThan = d
		default:
			return flags, fmt.Errorf("cleanup: unknown argument %q", arg)
		}
	}
	return flags, nil
}

func parseReconcileArgs(args []string) (reconcileFlags, error) {
	var flags reconcileFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dry-run":
			flags.dryRun = true
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return flags, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return flags, err
			}
			flags.inboundID = &inboundID
		default:
			return flags, fmt.Errorf("reconcile: unknown argument %q", arg)
		}
	}
	return flags, nil
}

func parseSelectorArgs(args []string) (engine.RuleSelector, error) {
	var selector engine.RuleSelector
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--email":
			value, next, err := readFlagValue(args, i, "--email")
			if err != nil {
				return selector, err
			}
			selector.Email = value
			i = next
		case strings.HasPrefix(arg, "--email="):
			selector.Email = strings.TrimPrefix(arg, "--email=")
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return selector, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return selector, err
			}
			selector.InboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return selector, err
			}
			selector.InboundID = &inboundID
		default:
			return selector, fmt.Errorf("unknown argument %q", arg)
		}
	}
	if strings.TrimSpace(selector.Email) == "" {
		return selector, errors.New("--email is required")
	}
	return selector, nil
}

func parseBulkSelectorArgs(args []string) (engine.BulkSelector, error) {
	var selector engine.BulkSelector
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				return selector, err
			}
			inboundID, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				return selector, err
			}
			selector.InboundID = &inboundID
			i = next
		case strings.HasPrefix(arg, "--inbound-id="):
			inboundID, err := parsePositiveInt64(strings.TrimPrefix(arg, "--inbound-id="), "--inbound-id")
			if err != nil {
				return selector, err
			}
			selector.InboundID = &inboundID
		default:
			return selector, fmt.Errorf("unknown argument %q", arg)
		}
	}
	return selector, nil
}

func readFlagValue(args []string, index int, name string) (string, int, error) {
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	return args[next], next, nil
}

func parsePositiveInt64(value, name string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func parsePositiveInt(value, name string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func parseDurationFlag(value, name string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		if !strings.HasSuffix(value, "d") {
			return 0, fmt.Errorf("%s must be a positive duration", name)
		}
		daysPart := strings.TrimSuffix(value, "d")
		days, daysErr := strconv.Atoi(daysPart)
		if strings.TrimSpace(daysPart) == "" || daysErr != nil {
			return 0, fmt.Errorf("%s must be a positive duration", name)
		}
		d = time.Duration(days) * 24 * time.Hour
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return d, nil
}

func commandErrorCode(err error) int {
	if errors.Is(err, store.ErrConflict) {
		return 1
	}
	return 1
}

func printRule(w io.Writer, rule store.Rule) {
	name := ""
	if rule.Name != "" {
		name = " name=" + rule.Name
	}
	if rule.Scope != nil {
		fmt.Fprintf(w, "rule=%d state=%s scope=%s factor=%s clients=%d%s\n",
			rule.ID,
			rule.State,
			store.ScopeDescription(rule.Scope),
			engine.FormatFactor(rule.FactorPPM),
			rule.ClientCount,
			name,
		)
		return
	}
	fmt.Fprintf(w, "rule=%d state=%s email=%s inbound=%d factor=%s clients=%d%s\n",
		rule.ID,
		rule.State,
		rule.Email,
		rule.InboundID,
		engine.FormatFactor(rule.FactorPPM),
		rule.ClientCount,
		name,
	)
}

func (a *App) printError(err error) {
	fmt.Fprintf(a.Err, "error: %v\n", err)
}

func (a *App) printTargetError(message, email string, inboundID *int64, hint string) {
	out := newOutput(a.Err)
	fmt.Fprintf(a.Err, "error: %s\n", message)
	out.Section("Target")
	out.Field("email", strings.TrimSpace(email))
	if inboundID != nil {
		out.Field("inbound", *inboundID)
	} else {
		out.Field("inbound", "any")
	}
	if hint != "" {
		out.Section("Hint")
		out.Field("run", hint)
	}
}

func (a *App) ensureWriters() {
	if a.Out == nil {
		a.Out = io.Discard
	}
	if a.Err == nil {
		a.Err = io.Discard
	}
}

func (a *App) printHelp(w io.Writer) {
	fmt.Fprintf(w, `%s

Usage:
  %s [--config PATH] [--database PATH] [command]

Commands:
  help       Show this help text
  version    Print version metadata
  doctor     Check database schema and metadata
  status     List effective active and paused rules
  explain    Explain one client's effective decision
  enable     Enable a factor rule
  enable-all Enable factor rules for selected clients
  exclude    Exclude one client from factor decisions
  unexclude  Disable an exclude policy
  excludes   List exclude policies
  override   Set one client's effective factor
  remove-override Disable an override policy
  overrides  List override policies
  disable    Disable a rule and keep existing results
  disable-all Disable active and paused rules
  pause      Pause a rule without changing counters
  pause-all  Pause active rules
  resume     Resume a paused rule from current counters
  resume-all Resume paused rules from current counters
  report     Show concise management report
  audit      Show lifecycle events
  backup     Create a timestamped SQLite backup
  cleanup    Prune stale XuiFactor metadata
  reconcile  Reconcile ineffective legacy rules
  tick       Apply one factor tick and exit
  run        Start the factor sidecar

Flags:
  -h, --help      Show this help text
  -v, --version   Print version metadata
  --config PATH   Load JSON config from PATH
  --database PATH Override configured SQLite database path

Examples:
  %s enable --email user@example.com --factor 1.2
  %s enable-all --factor 1.2
  %s enable-all --factor 1.2 --limited-only
  %s enable-all --factor 1.2 --once
  %s explain --email user@example.com --inbound-id 1
  %s status --effective
  %s status --clients --inbound-id 1
  %s exclude --email user@example.com --inbound-id 1
  %s excludes
  %s override --email user@example.com --inbound-id 1 --factor 1.2
  %s overrides
  %s report
  %s report --inbound-id 1
  %s audit --event traffic_applied --limit 20
  %s audit --since 24h
  %s disable --email user@example.com
  %s disable-all
  %s backup
  %s cleanup --dry-run
  %s reconcile --dry-run
  %s doctor
  %s tick
  %s run
`, displayName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName)
}

func (a *App) printVersion(w io.Writer) {
	fmt.Fprintf(w, "%s %s\ncommit: %s\nbuilt: %s\n", displayName, a.Info.Version, a.Info.Commit, a.Info.BuildTime)
}
