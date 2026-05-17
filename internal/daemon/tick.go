package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"time"

	"github.com/PdYrust/XuiFactor/internal/config"
	"github.com/PdYrust/XuiFactor/internal/engine"
	"github.com/PdYrust/XuiFactor/internal/policy"
	"github.com/PdYrust/XuiFactor/internal/store"
)

type Runner struct {
	store *store.Store
	cfg   config.Config
	out   io.Writer
	err   io.Writer
	now   func() time.Time

	beforeCounterUpdate func(context.Context, *store.Tx, store.ActiveRuleClient, store.ClientTraffic) error
}

type TickResult struct {
	ActiveClients int
	Reconciled    int
	Enrolled      int
	EnrollSkipped int
	Applied       int
	Baselined     int
	Rebaselined   int
	Missing       int
}

func New(st *store.Store, cfg config.Config, out, err io.Writer) *Runner {
	if out == nil {
		out = io.Discard
	}
	if err == nil {
		err = io.Discard
	}
	return &Runner{
		store: st,
		cfg:   cfg,
		out:   out,
		err:   err,
		now:   time.Now,
	}
}

func NewWithClock(st *store.Store, cfg config.Config, out, err io.Writer, now func() time.Time) *Runner {
	r := New(st, cfg, out, err)
	if now != nil {
		r.now = now
	}
	return r
}

func (r TickResult) HasWork() bool {
	return r.Reconciled > 0 || r.Enrolled > 0 || r.EnrollSkipped > 0 || r.Applied > 0 || r.Baselined > 0 || r.Rebaselined > 0 || r.Missing > 0
}

func (r TickResult) Summary() string {
	return fmt.Sprintf("active=%d reconciled=%d enrolled=%d enroll_skipped=%d applied=%d baselined=%d rebaselined=%d missing=%d",
		r.ActiveClients,
		r.Reconciled,
		r.Enrolled,
		r.EnrollSkipped,
		r.Applied,
		r.Baselined,
		r.Rebaselined,
		r.Missing,
	)
}

func (r *Runner) Tick(ctx context.Context) (TickResult, error) {
	now := r.now().Unix()
	var result TickResult

	err := r.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		reconciled, err := engine.ReconcileTx(ctx, tx, now, engine.ReconcileRequest{})
		if err != nil {
			return err
		}
		result.Reconciled = reconciled.Reconciled

		enrolled, skipped, err := r.reconcileScopes(ctx, tx, now)
		if err != nil {
			return err
		}
		result.Enrolled = enrolled
		result.EnrollSkipped = skipped

		targets, err := tx.ListActiveRuleClients(ctx)
		if err != nil {
			return err
		}
		excludes, err := tx.ListActiveExcludes(ctx)
		if err != nil {
			return err
		}
		candidates := policy.ActiveRuleCandidates(targets)
		candidates = append(candidates, policy.ExcludeCandidatesForActiveTargets(excludes, targets)...)
		decisions, err := policy.Decide(candidates)
		if err != nil {
			if errors.Is(err, policy.ErrAmbiguousEffectiveTarget) {
				return store.ConflictError("%v", err)
			}
			return err
		}

		for _, decision := range decisions {
			if decision.Target == nil {
				for _, suppressed := range decision.Suppressed {
					if err := r.refreshSuppressedBaseline(ctx, tx, suppressed, now); err != nil {
						return err
					}
				}
				continue
			}
			step, err := r.tickTarget(ctx, tx, *decision.Target, now)
			if err != nil {
				return err
			}
			result.ActiveClients++
			result.Applied += step.Applied
			result.Baselined += step.Baselined
			result.Rebaselined += step.Rebaselined
			result.Missing += step.Missing
			for _, suppressed := range decision.Suppressed {
				if err := r.refreshSuppressedBaseline(ctx, tx, suppressed, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return TickResult{}, fmt.Errorf("tick aborted and transaction rolled back: %w", err)
	}
	r.runAutoCleanup(ctx, now)
	return result, nil
}

func (r *Runner) refreshSuppressedBaseline(ctx context.Context, tx *store.Tx, target store.ActiveRuleClient, now int64) error {
	current, err := tx.ClientTrafficByID(ctx, target.Client.TrafficID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_, markErr := r.markMissing(ctx, tx, target, now)
			return markErr
		}
		return err
	}
	if current.InboundID != target.Client.InboundID || current.Email != target.Client.Email {
		_, markErr := r.markMissing(ctx, tx, target, now)
		return markErr
	}
	return tx.UpdateRuleClientBaseline(ctx, baseline(target, current, 0, 0, 0, now))
}

func (r *Runner) runAutoCleanup(ctx context.Context, now int64) {
	if !r.cfg.AutoCleanup {
		return
	}
	result, ran, err := r.store.AutoCleanup(ctx, store.CleanupOptions{
		Now:                   now,
		MissingClientGrace:    cleanupDurationSeconds(r.cfg.MissingClientGrace),
		DisabledRuleRetention: cleanupDurationSeconds(r.cfg.DisabledRuleRetention),
		AuditRetention:        cleanupDurationSeconds(r.cfg.AuditRetention),
	})
	if err != nil {
		fmt.Fprintf(r.err, "cleanup: error: %v\n", err)
		return
	}
	if ran && (result.MissingClientsPruned > 0 || result.DisabledRulesPruned > 0 || result.DisabledScopesPruned > 0 || result.InactiveExcludesPruned > 0 || result.AuditEventsPruned > 0) {
		fmt.Fprintf(r.out, "cleanup: missing_clients_pruned=%d disabled_rules_pruned=%d disabled_scopes_pruned=%d inactive_excludes_pruned=%d audit_events_pruned=%d vacuum_run=false\n",
			result.MissingClientsPruned,
			result.DisabledRulesPruned,
			result.DisabledScopesPruned,
			result.InactiveExcludesPruned,
			result.AuditEventsPruned,
		)
	}
}

func cleanupDurationSeconds(d time.Duration) int64 {
	seconds := int64(d / time.Second)
	if seconds <= 0 {
		return 1
	}
	return seconds
}

func (r *Runner) reconcileScopes(ctx context.Context, tx *store.Tx, now int64) (int, int, error) {
	scopes, err := tx.ListActivePersistentScopes(ctx)
	if err != nil {
		return 0, 0, err
	}

	enrolled := 0
	skipped := 0
	for _, scope := range scopes {
		clients, err := tx.ListClientCandidates(ctx, store.ClientFilter{
			InboundID:              scope.InboundID,
			LimitedOnly:            scope.LimitedOnly,
			IncludeDisabledClients: scope.IncludeDisabledClients,
		})
		if err != nil {
			return 0, 0, err
		}

		seen := make(map[int64]struct{}, len(clients))
		for _, client := range clients {
			if _, ok := seen[client.ID]; ok {
				message := fmt.Sprintf("traffic id %d skipped; duplicate candidate", client.ID)
				if err := tx.AddEvent(ctx, &scope.RuleID, store.EventScopeSkip, message, now); err != nil {
					return 0, 0, err
				}
				skipped++
				continue
			}
			seen[client.ID] = struct{}{}

			exists, err := tx.RuleClientExists(ctx, scope.RuleID, client.ID)
			if err != nil {
				return 0, 0, err
			}
			if exists {
				continue
			}

			conflict, err := tx.ActiveRuleForTrafficExcept(ctx, client.ID, scope.RuleID)
			if err == nil {
				message := fmt.Sprintf("traffic id %d skipped; active rule %d already targets it", client.ID, conflict.ID)
				if err := tx.AddEvent(ctx, &scope.RuleID, store.EventScopeSkip, message, now); err != nil {
					return 0, 0, err
				}
				skipped++
				continue
			}
			if !errors.Is(err, store.ErrNotFound) {
				return 0, 0, err
			}

			if err := tx.CreateRuleClient(ctx, store.RuleClient{
				RuleID:      scope.RuleID,
				TrafficID:   client.ID,
				InboundID:   client.InboundID,
				Email:       client.Email,
				LastUp:      client.Up,
				LastDown:    client.Down,
				LastAllTime: client.AllTime,
				UpdatedAt:   now,
			}); err != nil {
				return 0, 0, err
			}
			message := fmt.Sprintf("traffic id %d auto-enrolled email=%s inbound=%d", client.ID, client.Email, client.InboundID)
			if err := tx.AddEvent(ctx, &scope.RuleID, store.EventScopeEnroll, message, now); err != nil {
				return 0, 0, err
			}
			enrolled++
		}
	}
	return enrolled, skipped, nil
}

type targetResult struct {
	Applied     int
	Baselined   int
	Rebaselined int
	Missing     int
}

func (r *Runner) tickTarget(ctx context.Context, tx *store.Tx, target store.ActiveRuleClient, now int64) (targetResult, error) {
	current, err := tx.ClientTrafficByID(ctx, target.Client.TrafficID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return r.markMissing(ctx, tx, target, now)
		}
		return targetResult{}, err
	}
	if current.InboundID != target.Client.InboundID || current.Email != target.Client.Email {
		return r.markMissing(ctx, tx, target, now)
	}
	if target.Client.MissingAt != nil {
		if err := tx.UpdateRuleClientBaseline(ctx, baseline(target, current, 0, 0, 0, now)); err != nil {
			return targetResult{}, err
		}
		message := fmt.Sprintf("traffic id %d returned; baseline refreshed", target.Client.TrafficID)
		if err := tx.AddEvent(ctx, &target.Rule.ID, store.EventClientReset, message, now); err != nil {
			return targetResult{}, err
		}
		return targetResult{Rebaselined: 1}, nil
	}

	rawUpDelta, upDecreased, err := counterDelta(current.Up, target.Client.LastUp)
	if err != nil {
		return targetResult{}, err
	}
	rawDownDelta, downDecreased, err := counterDelta(current.Down, target.Client.LastDown)
	if err != nil {
		return targetResult{}, err
	}
	_, allTimeDecreased, err := counterDelta(current.AllTime, target.Client.LastAllTime)
	if err != nil {
		return targetResult{}, err
	}
	if upDecreased || downDecreased || allTimeDecreased {
		return r.rebaseline(ctx, tx, target, current, now)
	}

	if target.Rule.FactorPPM == engine.FactorScale {
		if current.Up == target.Client.LastUp &&
			current.Down == target.Client.LastDown &&
			current.AllTime == target.Client.LastAllTime &&
			target.Client.MissingAt == nil &&
			target.Client.RemUp == 0 &&
			target.Client.RemDown == 0 &&
			target.Client.RemAllTime == 0 {
			return targetResult{}, nil
		}
		if err := tx.UpdateRuleClientBaseline(ctx, baseline(target, current, 0, 0, 0, now)); err != nil {
			return targetResult{}, err
		}
		return targetResult{Baselined: 1}, nil
	}

	if rawUpDelta == 0 && rawDownDelta == 0 {
		if current.AllTime != target.Client.LastAllTime || target.Client.MissingAt != nil {
			if err := tx.UpdateRuleClientBaseline(ctx, baseline(target, current, target.Client.RemUp, target.Client.RemDown, target.Client.RemAllTime, now)); err != nil {
				return targetResult{}, err
			}
			return targetResult{Baselined: 1}, nil
		}
		return targetResult{}, nil
	}

	factorExtra := target.Rule.FactorPPM - engine.FactorScale
	extraUp, remUp, err := calculateExtra(rawUpDelta, factorExtra, target.Client.RemUp)
	if err != nil {
		return targetResult{}, err
	}
	extraDown, remDown, err := calculateExtra(rawDownDelta, factorExtra, target.Client.RemDown)
	if err != nil {
		return targetResult{}, err
	}
	extraAllTime, err := safeAdd(extraUp, extraDown)
	if err != nil {
		return targetResult{}, err
	}

	if extraUp == 0 && extraDown == 0 {
		if err := tx.UpdateRuleClientBaseline(ctx, baseline(target, current, remUp, remDown, target.Client.RemAllTime, now)); err != nil {
			return targetResult{}, err
		}
		return targetResult{Baselined: 1}, nil
	}

	newUp, err := safeAdd(current.Up, extraUp)
	if err != nil {
		return targetResult{}, err
	}
	newDown, err := safeAdd(current.Down, extraDown)
	if err != nil {
		return targetResult{}, err
	}
	newAllTime, err := safeAdd(current.AllTime, extraAllTime)
	if err != nil {
		return targetResult{}, err
	}

	if r.beforeCounterUpdate != nil {
		if err := r.beforeCounterUpdate(ctx, tx, target, current); err != nil {
			return targetResult{}, err
		}
	}

	ok, err := tx.UpdateClientCountersCAS(ctx, current, newUp, newDown, newAllTime)
	if err != nil {
		return targetResult{}, err
	}
	if !ok {
		return targetResult{}, store.ErrRace
	}

	if err := tx.UpdateRuleClientBaseline(ctx, store.RuleClient{
		RuleID:      target.Client.RuleID,
		TrafficID:   target.Client.TrafficID,
		LastUp:      newUp,
		LastDown:    newDown,
		LastAllTime: newAllTime,
		RemUp:       remUp,
		RemDown:     remDown,
		RemAllTime:  target.Client.RemAllTime,
		UpdatedAt:   now,
	}); err != nil {
		return targetResult{}, err
	}
	message := fmt.Sprintf("traffic id %d extra_up=%d extra_down=%d extra_all_time=%d", target.Client.TrafficID, extraUp, extraDown, extraAllTime)
	if err := tx.AddEvent(ctx, &target.Rule.ID, store.EventTrafficApply, message, now); err != nil {
		return targetResult{}, err
	}
	return targetResult{Applied: 1}, nil
}

func (r *Runner) markMissing(ctx context.Context, tx *store.Tx, target store.ActiveRuleClient, now int64) (targetResult, error) {
	if err := tx.MarkRuleClientMissing(ctx, target.Client.RuleID, target.Client.TrafficID, now); err != nil {
		return targetResult{}, err
	}
	if target.Client.MissingAt == nil {
		message := fmt.Sprintf("traffic id %d is missing or no longer matches rule target", target.Client.TrafficID)
		if err := tx.AddEvent(ctx, &target.Rule.ID, store.EventClientMissing, message, now); err != nil {
			return targetResult{}, err
		}
	}
	return targetResult{Missing: 1}, nil
}

func (r *Runner) rebaseline(ctx context.Context, tx *store.Tx, target store.ActiveRuleClient, current store.ClientTraffic, now int64) (targetResult, error) {
	if err := tx.UpdateRuleClientBaseline(ctx, baseline(target, current, 0, 0, 0, now)); err != nil {
		return targetResult{}, err
	}
	message := fmt.Sprintf("traffic id %d counters decreased; baseline refreshed", target.Client.TrafficID)
	if err := tx.AddEvent(ctx, &target.Rule.ID, store.EventClientReset, message, now); err != nil {
		return targetResult{}, err
	}
	return targetResult{Rebaselined: 1}, nil
}

func baseline(target store.ActiveRuleClient, current store.ClientTraffic, remUp, remDown, remAllTime, now int64) store.RuleClient {
	return store.RuleClient{
		RuleID:      target.Client.RuleID,
		TrafficID:   target.Client.TrafficID,
		LastUp:      current.Up,
		LastDown:    current.Down,
		LastAllTime: current.AllTime,
		RemUp:       remUp,
		RemDown:     remDown,
		RemAllTime:  remAllTime,
		UpdatedAt:   now,
	}
}

func rejectDuplicateTargets(targets []store.ActiveRuleClient) error {
	seen := make(map[int64]int64, len(targets))
	for _, target := range targets {
		if ruleID, ok := seen[target.Client.TrafficID]; ok {
			return store.ConflictError("active rules %d and %d both target traffic id %d", ruleID, target.Rule.ID, target.Client.TrafficID)
		}
		seen[target.Client.TrafficID] = target.Rule.ID
	}
	return nil
}

func calculateExtra(delta, factorExtra, remainder int64) (int64, int64, error) {
	if delta < 0 || factorExtra < 0 || remainder < 0 {
		return 0, 0, errors.New("invalid factor input")
	}
	value := new(big.Int).Mul(big.NewInt(delta), big.NewInt(factorExtra))
	value.Add(value, big.NewInt(remainder))

	scale := big.NewInt(engine.FactorScale)
	quotient := new(big.Int)
	modulo := new(big.Int)
	quotient.QuoRem(value, scale, modulo)
	if !quotient.IsInt64() || quotient.Int64() > math.MaxInt64 {
		return 0, 0, errors.New("factor result overflow")
	}
	return quotient.Int64(), modulo.Int64(), nil
}

func counterDelta(current, last int64) (int64, bool, error) {
	if current < 0 || last < 0 {
		return 0, false, errors.New("counter value is negative")
	}
	if current < last {
		return 0, true, nil
	}
	return current - last, false, nil
}

func safeAdd(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, errors.New("counter overflow")
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, errors.New("counter overflow")
	}
	return a + b, nil
}
