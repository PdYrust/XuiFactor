package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PdYrust/XuiFactor/internal/store"
)

type Service struct {
	store *store.Store
	now   func() time.Time
}

type EnableRequest struct {
	Name      string
	Email     string
	InboundID *int64
	Factor    string
}

type RuleSelector struct {
	Email     string
	InboundID *int64
}

type AuditRequest struct {
	Limit     int
	Email     string
	InboundID *int64
}

func New(st *store.Store) *Service {
	return &Service{
		store: st,
		now:   time.Now,
	}
}

func NewWithClock(st *store.Store, now func() time.Time) *Service {
	svc := New(st)
	if now != nil {
		svc.now = now
	}
	return svc
}

func (s *Service) Enable(ctx context.Context, req EnableRequest) (store.Rule, error) {
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return store.Rule{}, err
	}
	factorPPM, err := ParseFactor(req.Factor)
	if err != nil {
		return store.Rule{}, err
	}

	now := s.now().Unix()
	var created store.Rule
	err = s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		client, err := tx.FindClient(ctx, email, req.InboundID)
		if err != nil {
			return describeSelectorError("find client", err)
		}

		conflict, err := tx.ActiveRuleForTraffic(ctx, client.ID)
		if err == nil {
			return store.ConflictError("active rule %d already targets traffic id %d", conflict.ID, client.ID)
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		ruleID, err := tx.CreateRule(ctx, store.Rule{
			Name:        strings.TrimSpace(req.Name),
			InboundID:   client.InboundID,
			Email:       client.Email,
			FactorPPM:   factorPPM,
			State:       store.StateActive,
			CreatedAt:   now,
			UpdatedAt:   now,
			ActivatedAt: &now,
		})
		if err != nil {
			return err
		}

		if err := tx.CreateRuleClient(ctx, store.RuleClient{
			RuleID:      ruleID,
			TrafficID:   client.ID,
			InboundID:   client.InboundID,
			Email:       client.Email,
			LastUp:      client.Up,
			LastDown:    client.Down,
			LastAllTime: client.AllTime,
			UpdatedAt:   now,
		}); err != nil {
			return err
		}

		if err := tx.AddEvent(ctx, &ruleID, store.EventRuleEnabled, "rule enabled", now); err != nil {
			return err
		}

		created, err = tx.GetRule(ctx, ruleID)
		return err
	})
	return created, err
}

func (s *Service) Disable(ctx context.Context, selector RuleSelector) (store.Rule, error) {
	return s.transition(ctx, selector, []string{store.StateActive, store.StatePaused}, store.StateDisabled)
}

func (s *Service) Pause(ctx context.Context, selector RuleSelector) (store.Rule, error) {
	return s.transition(ctx, selector, []string{store.StateActive}, store.StatePaused)
}

func (s *Service) Resume(ctx context.Context, selector RuleSelector) (store.Rule, error) {
	email, err := normalizeEmail(selector.Email)
	if err != nil {
		return store.Rule{}, err
	}
	now := s.now().Unix()
	var updated store.Rule

	err = s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		rule, err := tx.FindRuleBySelector(ctx, email, selector.InboundID, store.StatePaused)
		if err != nil {
			return describeSelectorError("find paused rule", err)
		}
		if conflict, trafficID, err := tx.ActiveConflictForRule(ctx, rule.ID); err == nil {
			return store.ConflictError("active rule %d already targets traffic id %d", conflict.ID, trafficID)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := tx.RefreshRuleClientBaselines(ctx, rule.ID, now); err != nil {
			return describeSelectorError("refresh rule baseline", err)
		}
		if err := tx.SetRuleActive(ctx, rule.ID, now); err != nil {
			return err
		}
		if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleResumed, "rule resumed", now); err != nil {
			return err
		}
		updated, err = tx.GetRule(ctx, rule.ID)
		return err
	})
	return updated, err
}

func (s *Service) transition(ctx context.Context, selector RuleSelector, from []string, to string) (store.Rule, error) {
	email, err := normalizeEmail(selector.Email)
	if err != nil {
		return store.Rule{}, err
	}
	now := s.now().Unix()
	var updated store.Rule

	err = s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		rule, err := tx.FindRuleBySelector(ctx, email, selector.InboundID, from...)
		if err != nil {
			return describeSelectorError("find rule", err)
		}

		switch to {
		case store.StateDisabled:
			if err := tx.SetRuleDisabled(ctx, rule.ID, now); err != nil {
				return err
			}
			if err := tx.AddEvent(ctx, &rule.ID, store.EventRuleDisabled, "rule disabled", now); err != nil {
				return err
			}
		case store.StatePaused:
			if err := tx.SetRulePaused(ctx, rule.ID, now); err != nil {
				return err
			}
			if err := tx.AddEvent(ctx, &rule.ID, store.EventRulePaused, "rule paused", now); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported rule state %q", to)
		}

		updated, err = tx.GetRule(ctx, rule.ID)
		return err
	})
	return updated, err
}

func (s *Service) Status(ctx context.Context, includeDisabled bool) ([]store.Rule, error) {
	return s.store.ListRules(ctx, includeDisabled)
}

func (s *Service) Audit(ctx context.Context, req AuditRequest) ([]store.Event, error) {
	return s.store.ListEvents(ctx, store.EventFilter{
		Limit:     req.Limit,
		Email:     strings.TrimSpace(req.Email),
		InboundID: req.InboundID,
	})
}

func normalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", errors.New("email is required")
	}
	return email, nil
}

func describeSelectorError(action string, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%s: no matching record", action)
	case errors.Is(err, store.ErrAmbiguous):
		return fmt.Errorf("%s: multiple matches; pass --inbound-id", action)
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
}
