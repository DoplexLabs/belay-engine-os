package localapp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const (
	maxMissionPackReceiptReconcileBatch = 256
	missionPackReceiptSessionProbeLimit = 2
)

type MissionPackReceiptReconciliationRepository interface {
	QueryUnresolvedMissionPackReceipts(
		context.Context,
		int,
	) ([]local.MissionPackReceipt, error)
	QueryCompatibleTranscriptSessionKeys(
		context.Context,
		string,
		experience.Harness,
		time.Time,
		time.Time,
		int,
	) ([]string, error)
	ResolveMissionPackReceipt(
		context.Context,
		string,
		[]string,
		time.Time,
	) (local.MissionPackReceipt, error)
}

type MissionPackReceiptReconciliationCoordinator struct {
	repository MissionPackReceiptReconciliationRepository
}

func NewMissionPackReceiptReconciliationCoordinator(
	repository MissionPackReceiptReconciliationRepository,
) (*MissionPackReceiptReconciliationCoordinator, error) {
	if repository == nil {
		return nil, errors.New(
			"mission pack receipt reconciliation requires a repository",
		)
	}
	return &MissionPackReceiptReconciliationCoordinator{
		repository: repository,
	}, nil
}

func (coordinator *MissionPackReceiptReconciliationCoordinator) Reconcile(
	ctx context.Context,
	observedAt time.Time,
	limit int,
) error {
	if ctx == nil {
		return errors.New(
			"mission pack receipt reconciliation requires a context",
		)
	}
	if coordinator == nil || coordinator.repository == nil {
		return errors.New(
			"mission pack receipt reconciliation requires a repository",
		)
	}
	if observedAt.IsZero() {
		return errors.New(
			"mission pack receipt reconciliation time is required",
		)
	}
	if limit < 1 || limit > maxMissionPackReceiptReconcileBatch {
		return fmt.Errorf(
			"mission pack receipt reconciliation limit must be between 1 and %d",
			maxMissionPackReceiptReconcileBatch,
		)
	}

	receipts, err := coordinator.repository.
		QueryUnresolvedMissionPackReceipts(ctx, limit)
	if err != nil {
		return fmt.Errorf("query unresolved mission pack receipts: %w", err)
	}
	sort.Slice(receipts, func(i, j int) bool {
		if receipts[i].AcceptedAt.Equal(receipts[j].AcceptedAt) {
			return receipts[i].ReceiptID < receipts[j].ReceiptID
		}
		return receipts[i].AcceptedAt.Before(receipts[j].AcceptedAt)
	})
	if len(receipts) > limit {
		receipts = receipts[:limit]
	}

	reconcileErrors := make([]error, 0)
	for _, receipt := range receipts {
		sessionKeys := []string(nil)
		if observedAt.Before(receipt.ExpiresAt) {
			sessionKeys, err = coordinator.repository.
				QueryCompatibleTranscriptSessionKeys(
					ctx,
					receipt.ProjectIdentity,
					receipt.Harness,
					receipt.AcceptedAt,
					receipt.ExpiresAt,
					missionPackReceiptSessionProbeLimit,
				)
			if err != nil {
				reconcileErrors = append(
					reconcileErrors,
					fmt.Errorf(
						"query compatible sessions for mission pack receipt %q: %w",
						receipt.ReceiptID,
						err,
					),
				)
				continue
			}
		}

		if _, err := coordinator.repository.ResolveMissionPackReceipt(
			ctx,
			receipt.ReceiptID,
			sessionKeys,
			observedAt,
		); err != nil {
			reconcileErrors = append(
				reconcileErrors,
				fmt.Errorf(
					"resolve mission pack receipt %q: %w",
					receipt.ReceiptID,
					err,
				),
			)
		}
	}
	return errors.Join(reconcileErrors...)
}
