package engine

import (
	"context"
	"errors"
	"time"

	"github.com/PdYrust/XuiFactor/internal/config"
	"github.com/PdYrust/XuiFactor/internal/store"
)

type CleanupRequest struct {
	Config    config.Config
	DryRun    bool
	OlderThan time.Duration
	Vacuum    bool
}

func (s *Service) Cleanup(ctx context.Context, req CleanupRequest) (store.CleanupResult, error) {
	missingGrace := req.Config.MissingClientGrace
	disabledRetention := req.Config.DisabledRuleRetention
	auditRetention := req.Config.AuditRetention
	if req.OlderThan > 0 {
		disabledRetention = req.OlderThan
		auditRetention = req.OlderThan
	}
	if missingGrace <= 0 || disabledRetention <= 0 || auditRetention <= 0 {
		return store.CleanupResult{}, errors.New("cleanup retention values must be positive")
	}

	result, err := s.store.Cleanup(ctx, store.CleanupOptions{
		Now:                   s.now().Unix(),
		MissingClientGrace:    cleanupSeconds(missingGrace),
		DisabledRuleRetention: cleanupSeconds(disabledRetention),
		AuditRetention:        cleanupSeconds(auditRetention),
		DryRun:                req.DryRun,
	})
	if err != nil {
		return store.CleanupResult{}, err
	}
	if req.Vacuum && !req.DryRun {
		if err := s.store.Vacuum(ctx); err != nil {
			return store.CleanupResult{}, err
		}
		result.VacuumRun = true
	}
	return result, nil
}

func cleanupSeconds(d time.Duration) int64 {
	seconds := int64(d / time.Second)
	if seconds <= 0 {
		return 1
	}
	return seconds
}
