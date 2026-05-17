package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/PdYrust/XuiFactor/internal/store"
)

type ExcludeRequest struct {
	Email     string
	InboundID *int64
	Note      string
}

type ExcludeSelector struct {
	Email     string
	InboundID *int64
}

type ExcludeListRequest struct {
	InboundID       *int64
	IncludeInactive bool
}

func (s *Service) Exclude(ctx context.Context, req ExcludeRequest) (store.ExcludePolicy, error) {
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return store.ExcludePolicy{}, err
	}
	if req.InboundID == nil {
		return store.ExcludePolicy{}, errors.New("inbound id is required")
	}

	now := s.now().Unix()
	var policy store.ExcludePolicy
	err = s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		client, err := tx.FindClient(ctx, email, req.InboundID)
		if err != nil {
			return describeSelectorError("find client", err)
		}
		activatedAt := now
		policy, err = tx.UpsertExclude(ctx, store.ExcludePolicy{
			TrafficID:   client.ID,
			InboundID:   client.InboundID,
			Email:       client.Email,
			State:       store.StateActive,
			Note:        strings.TrimSpace(req.Note),
			CreatedAt:   now,
			UpdatedAt:   now,
			ActivatedAt: &activatedAt,
		})
		if err != nil {
			return err
		}
		message := fmt.Sprintf("exclude policy %d enabled for traffic id %d inbound %d email %s", policy.ID, policy.TrafficID, policy.InboundID, policy.Email)
		return tx.AddEvent(ctx, nil, store.EventExcludeEnable, message, now)
	})
	return policy, err
}

func (s *Service) Unexclude(ctx context.Context, selector ExcludeSelector) (store.ExcludePolicy, error) {
	email, err := normalizeEmail(selector.Email)
	if err != nil {
		return store.ExcludePolicy{}, err
	}
	if selector.InboundID == nil {
		return store.ExcludePolicy{}, errors.New("inbound id is required")
	}

	now := s.now().Unix()
	var policy store.ExcludePolicy
	err = s.store.WithImmediateTx(ctx, func(tx *store.Tx) error {
		client, err := tx.FindClient(ctx, email, selector.InboundID)
		if err != nil {
			return describeSelectorError("find client", err)
		}
		policy, err = tx.FindActiveExcludeForClient(ctx, client.ID, client.InboundID, client.Email)
		if err != nil {
			return describeSelectorError("find exclude policy", err)
		}
		if err := tx.DisableExclude(ctx, policy.ID, now); err != nil {
			return err
		}
		message := fmt.Sprintf("exclude policy %d disabled for traffic id %d inbound %d email %s", policy.ID, policy.TrafficID, policy.InboundID, policy.Email)
		if err := tx.AddEvent(ctx, nil, store.EventExcludeDisable, message, now); err != nil {
			return err
		}
		policy, err = tx.FindExcludeByIdentity(ctx, client.ID, client.InboundID, client.Email)
		return err
	})
	return policy, err
}

func (s *Service) Excludes(ctx context.Context, req ExcludeListRequest) ([]store.ExcludePolicy, error) {
	return s.store.ListExcludes(ctx, req.IncludeInactive, req.InboundID)
}
