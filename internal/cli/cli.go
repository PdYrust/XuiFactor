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
	case "enable":
		return a.runEnable(ctx, common, args[1:])
	case "enable-all":
		return a.runEnableAll(ctx, common, args[1:])
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

	fmt.Fprintf(a.Out, "%s doctor\n", displayName)
	fmt.Fprintf(a.Out, "version: %s\n", a.Info.Version)
	fmt.Fprintf(a.Out, "commit: %s\n", a.Info.Commit)
	fmt.Fprintf(a.Out, "built: %s\n", a.Info.BuildTime)
	fmt.Fprintf(a.Out, "config: %s\n", effectiveConfigPath(opts))
	fmt.Fprintf(a.Out, "database: %s\n", cfg.DatabasePath)

	dbAccess, err := checkDatabaseFile(cfg.DatabasePath)
	if err != nil {
		a.printError(err)
		return 1
	}
	fmt.Fprintln(a.Out, "OK database read")
	if dbAccess.Writable {
		fmt.Fprintln(a.Out, "OK database write")
	} else {
		fmt.Fprintf(a.Out, "WARN database write unavailable: %v\n", dbAccess.WriteError)
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
	fmt.Fprintln(a.Out, "OK schema")
	metadataReady := false
	ready, err := st.MetadataReady(ctx)
	if err != nil {
		a.printError(err)
		return 1
	}
	metadataReady = ready
	if ready {
		fmt.Fprintln(a.Out, "OK metadata")
	} else {
		fmt.Fprintln(a.Out, "WARN metadata unavailable: metadata tables are missing")
	}

	if err := validateBackupDir(cfg.BackupDir); err != nil {
		a.printError(err)
		return 1
	}
	fmt.Fprintf(a.Out, "OK backup dir %s\n", cfg.BackupDir)

	service := systemdServiceState()
	fmt.Fprintf(a.Out, "service installed: %s\n", service.Installed)
	fmt.Fprintf(a.Out, "service enabled: %s\n", service.Enabled)
	fmt.Fprintf(a.Out, "service active: %s\n", service.Active)

	if metadataReady {
		counts, err := st.CountRules(ctx)
		if err != nil {
			a.printError(err)
			return 1
		}
		fmt.Fprintf(a.Out, "OK rules active=%d paused=%d disabled=%d\n", counts.Active, counts.Paused, counts.Disabled)
		if (counts.Active > 0 || counts.Paused > 0) && (service.Active != "yes" || service.Enabled == "no") {
			fmt.Fprintln(a.Out, "warning: active rules exist but xui-factor.service is not running")
		}
		persistentScopes, err := st.CountActivePersistentScopes(ctx)
		if err != nil {
			a.printError(err)
			return 1
		}
		if persistentScopes > 0 && service.Active != "yes" {
			fmt.Fprintln(a.Out, "warning: persistent scopes exist but future client auto-enrollment requires xui-factor.service")
		}
	} else {
		fmt.Fprintln(a.Out, "WARN rules unavailable: metadata tables are missing")
	}
	fmt.Fprintln(a.Out, "doctor: OK")
	return 0
}

func (a *App) runStatus(ctx context.Context, opts commonOptions, args []string) int {
	includeDisabled := false
	for _, arg := range args {
		switch arg {
		case "--include-disabled", "--all":
			includeDisabled = true
		default:
			a.printError(fmt.Errorf("status: unknown argument %q", arg))
			return 2
		}
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
		fmt.Fprintln(a.Out, "status: no active or paused rules")
		return 0
	}

	rules, err := svc.Status(ctx, includeDisabled)
	if err != nil {
		a.printError(err)
		return 1
	}
	if len(rules) == 0 {
		if includeDisabled {
			fmt.Fprintln(a.Out, "status: no rules")
		} else {
			fmt.Fprintln(a.Out, "status: no active or paused rules")
		}
		return 0
	}
	if includeDisabled {
		fmt.Fprintln(a.Out, "status: rules including inactive")
	} else {
		fmt.Fprintln(a.Out, "status: effective active and paused rules")
	}
	for _, rule := range rules {
		printRule(a.Out, rule)
	}
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
		a.printError(err)
		return commandErrorCode(err)
	}
	fmt.Fprintf(a.Out, "enable: active rule=%d email=%s inbound=%d factor=%s\n", rule.ID, rule.Email, rule.InboundID, engine.FormatFactor(rule.FactorPPM))
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
	fmt.Fprintf(a.Out, "enable-all: %s\n", result.Summary())
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
	fmt.Fprintf(a.Out, "%s: %s rule=%d email=%s inbound=%d\n", command, resultState, rule.ID, rule.Email, rule.InboundID)
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
	fmt.Fprintf(a.Out, "%s: %s\n", label, result.Summary())
	return 0
}

func (a *App) runAudit(ctx context.Context, opts commonOptions, args []string) int {
	limit := 50
	var email string
	var inboundID *int64
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--limit":
			value, next, err := readFlagValue(args, i, "--limit")
			if err != nil {
				a.printError(err)
				return 2
			}
			limit, err = strconv.Atoi(value)
			if err != nil || limit <= 0 {
				a.printError(errors.New("audit: --limit must be a positive integer"))
				return 2
			}
			i = next
		case strings.HasPrefix(args[i], "--limit="):
			value := strings.TrimPrefix(args[i], "--limit=")
			var err error
			limit, err = strconv.Atoi(value)
			if err != nil || limit <= 0 {
				a.printError(errors.New("audit: --limit must be a positive integer"))
				return 2
			}
		case args[i] == "--email":
			value, next, err := readFlagValue(args, i, "--email")
			if err != nil {
				a.printError(err)
				return 2
			}
			email = value
			i = next
		case strings.HasPrefix(args[i], "--email="):
			email = strings.TrimPrefix(args[i], "--email=")
		case args[i] == "--inbound-id":
			value, next, err := readFlagValue(args, i, "--inbound-id")
			if err != nil {
				a.printError(err)
				return 2
			}
			id, err := parsePositiveInt64(value, "--inbound-id")
			if err != nil {
				a.printError(err)
				return 2
			}
			inboundID = &id
			i = next
		case strings.HasPrefix(args[i], "--inbound-id="):
			id, err := parsePositiveInt64(strings.TrimPrefix(args[i], "--inbound-id="), "--inbound-id")
			if err != nil {
				a.printError(err)
				return 2
			}
			inboundID = &id
		default:
			a.printError(fmt.Errorf("audit: unknown argument %q", args[i]))
			return 2
		}
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
		fmt.Fprintln(a.Out, "audit: no events")
		return 0
	}

	events, err := svc.Audit(ctx, engine.AuditRequest{
		Limit:     limit,
		Email:     email,
		InboundID: inboundID,
	})
	if err != nil {
		a.printError(err)
		return 1
	}
	if len(events) == 0 {
		fmt.Fprintln(a.Out, "audit: no events")
		return 0
	}
	for _, event := range events {
		ruleID := "-"
		if event.RuleID != nil {
			ruleID = strconv.FormatInt(*event.RuleID, 10)
		}
		ts := time.Unix(event.CreatedAt, 0).UTC().Format(time.RFC3339)
		fmt.Fprintf(a.Out, "%s rule=%s type=%s %s\n", ts, ruleID, event.EventType, event.Message)
	}
	return 0
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
	fmt.Fprintf(a.Out, "backup: created path=%s\n", path)
	fmt.Fprintln(a.Out, "restore: manual only; stop x-ui and xui-factor before replacing the database")
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
	fmt.Fprintf(a.Out, "cleanup: missing_clients_pruned=%d disabled_rules_pruned=%d disabled_scopes_pruned=%d audit_events_pruned=%d vacuum_run=%t\n",
		result.MissingClientsPruned,
		result.DisabledRulesPruned,
		result.DisabledScopesPruned,
		result.AuditEventsPruned,
		result.VacuumRun,
	)
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
	fmt.Fprintf(a.Out, "reconcile: %s\n", result.Summary())
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
	fmt.Fprintf(a.Out, "tick: %s\n", result.Summary())
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
  enable     Enable a factor rule
  enable-all Enable factor rules for selected clients
  disable    Disable a rule and keep existing results
  disable-all Disable active and paused rules
  pause      Pause a rule without changing counters
  pause-all  Pause active rules
  resume     Resume a paused rule from current counters
  resume-all Resume paused rules from current counters
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
  %s disable --email user@example.com
  %s disable-all
  %s backup
  %s cleanup --dry-run
  %s reconcile --dry-run
  %s doctor
  %s tick
  %s run
`, displayName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName)
}

func (a *App) printVersion(w io.Writer) {
	fmt.Fprintf(w, "%s %s\ncommit: %s\nbuilt: %s\n", displayName, a.Info.Version, a.Info.Commit, a.Info.BuildTime)
}
