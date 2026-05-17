package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/PdYrust/XuiFactor/internal/store"
)

type OverrideRequest struct {
	Email     string
	InboundID *int64
	Factor    string
	Note      string
}

type OverrideSelector struct {
	Email     string
	InboundID *int64
}

type OverrideListRequest struct {
	InboundID       *int64
	IncludeInactive bool
}

func (s *Service) Override(ctx context.Context, req OverrideRequest) (store.OverridePolicy, error) {
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return store.OverridePolicy{}, err
	}
	if req.InboundID == nil {
		return store.OverridePolicy{}, errors.New("inbound id is required")
	}
	factorPPM, err := ParseFactor(req.Factor)
	if err != nil {
		return store.OverridePolicy{}, err
	}

	now := s.now().Unix()
	var policy store.OverridePolicy
	var updated bool
	err = s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		client, err := tx.FindClient(ctx, email, req.InboundID)
		if err != nil {
			return describeSelectorError("find client", err)
		}
		activatedAt := now
		policy, updated, err = tx.UpsertOverride(ctx, store.OverridePolicy{
			TrafficID:   client.ID,
			InboundID:   client.InboundID,
			Email:       client.Email,
			FactorPPM:   factorPPM,
			State:       store.StateActive,
			Note:        strings.TrimSpace(req.Note),
			CreatedAt:   now,
			UpdatedAt:   now,
			ActivatedAt: &activatedAt,
		})
		if err != nil {
			return err
		}
		if _, err := tx.RefreshActiveRuleClientBaselinesForTraffic(ctx, client.ID, client.InboundID, client.Email, now); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		eventType := store.EventOverrideEnable
		action := "enabled"
		if updated {
			eventType = store.EventOverrideUpdate
			action = "updated"
		}
		message := fmt.Sprintf("override policy %d %s for traffic id %d inbound %d email %s factor %s", policy.ID, action, policy.TrafficID, policy.InboundID, policy.Email, FormatFactor(policy.FactorPPM))
		return tx.AddEvent(ctx, nil, eventType, message, now)
	})
	return policy, err
}

func (s *Service) RemoveOverride(ctx context.Context, selector OverrideSelector) (store.OverridePolicy, error) {
	email, err := normalizeEmail(selector.Email)
	if err != nil {
		return store.OverridePolicy{}, err
	}
	if selector.InboundID == nil {
		return store.OverridePolicy{}, errors.New("inbound id is required")
	}

	now := s.now().Unix()
	var policy store.OverridePolicy
	err = s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		client, err := tx.FindClient(ctx, email, selector.InboundID)
		if err != nil {
			return describeSelectorError("find client", err)
		}
		policy, err = tx.FindActiveOverrideForClient(ctx, client.ID, client.InboundID, client.Email)
		if err != nil {
			return describeSelectorError("find override policy", err)
		}
		if err := tx.DisableOverride(ctx, policy.ID, now); err != nil {
			return err
		}
		if _, err := tx.RefreshActiveRuleClientBaselinesForTraffic(ctx, client.ID, client.InboundID, client.Email, now); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		message := fmt.Sprintf("override policy %d disabled for traffic id %d inbound %d email %s", policy.ID, policy.TrafficID, policy.InboundID, policy.Email)
		if err := tx.AddEvent(ctx, nil, store.EventOverrideDisable, message, now); err != nil {
			return err
		}
		policy, err = tx.FindOverrideByIdentity(ctx, client.ID, client.InboundID, client.Email)
		return err
	})
	return policy, err
}

func (s *Service) Overrides(ctx context.Context, req OverrideListRequest) ([]store.OverridePolicy, error) {
	return s.store.ListOverrides(ctx, req.IncludeInactive, req.InboundID)
}
