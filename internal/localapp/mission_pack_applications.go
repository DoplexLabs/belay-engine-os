package localapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const maxMissionPackApplicationMaterializationBatch = 256

type MissionPackApplicationMaterializationRepository interface {
	QueryBoundMissionPackReceiptsNeedingApplications(
		context.Context,
		int,
	) ([]local.MissionPackReceipt, error)
	EnsureMissionPackReceiptApplicationProgress(
		context.Context,
		local.MissionPackReceipt,
	) error
	InsertExperienceApplication(
		context.Context,
		experience.Application,
	) (bool, error)
	LinkMissionPackReceiptApplication(
		context.Context,
		string,
		experience.ExperienceRef,
		string,
	) error
}

type MissionPackApplicationMaterializationReport struct {
	ReceiptsAttempted    int
	ApplicationsInserted int
	ApplicationsReplayed int
}

type MissionPackApplicationMaterializationCoordinator struct {
	repository MissionPackApplicationMaterializationRepository
}

func NewMissionPackApplicationMaterializationCoordinator(
	repository MissionPackApplicationMaterializationRepository,
) (*MissionPackApplicationMaterializationCoordinator, error) {
	if repository == nil {
		return nil, errors.New(
			"mission pack application materialization requires a repository",
		)
	}
	return &MissionPackApplicationMaterializationCoordinator{
		repository: repository,
	}, nil
}

func (coordinator *MissionPackApplicationMaterializationCoordinator) Materialize(
	ctx context.Context,
	limit int,
) (MissionPackApplicationMaterializationReport, error) {
	var report MissionPackApplicationMaterializationReport
	if ctx == nil {
		return report, errors.New(
			"mission pack application materialization requires a context",
		)
	}
	if coordinator == nil || coordinator.repository == nil {
		return report, errors.New(
			"mission pack application materialization requires a repository",
		)
	}
	if limit < 1 || limit > maxMissionPackApplicationMaterializationBatch {
		return report, fmt.Errorf(
			"mission pack application materialization limit must be between 1 and %d",
			maxMissionPackApplicationMaterializationBatch,
		)
	}

	receipts, err := coordinator.repository.
		QueryBoundMissionPackReceiptsNeedingApplications(ctx, limit)
	if err != nil {
		return report, fmt.Errorf(
			"query mission pack receipts needing application materialization: %w",
			err,
		)
	}
	if len(receipts) > limit {
		receipts = receipts[:limit]
	}

	materializationErrors := make([]error, 0)
	for _, receipt := range receipts {
		report.ReceiptsAttempted++
		receiptLabel := receiptDiagnosticLabel(receipt.ReceiptID)
		if err := validateBoundMissionPackReceiptForMaterialization(receipt); err != nil {
			materializationErrors = append(
				materializationErrors,
				fmt.Errorf(
					"materialize mission pack receipt %s: %w",
					receiptLabel,
					err,
				),
			)
			continue
		}
		if err := coordinator.repository.
			EnsureMissionPackReceiptApplicationProgress(ctx, receipt); err != nil {
			materializationErrors = append(
				materializationErrors,
				fmt.Errorf(
					"initialize mission pack receipt %s application progress: %w",
					receiptLabel,
					err,
				),
			)
			continue
		}

		for _, ref := range receipt.ExperienceRefs {
			application := deliveredMissionPackApplication(receipt, ref)
			if err := application.Validate(); err != nil {
				materializationErrors = append(
					materializationErrors,
					fmt.Errorf(
						"materialize mission pack receipt %s experience %s:%d: invalid application",
						receiptLabel,
						ref.ExperienceID,
						ref.Version,
					),
				)
				continue
			}
			inserted, err := coordinator.repository.InsertExperienceApplication(
				ctx,
				application,
			)
			if err != nil {
				materializationErrors = append(
					materializationErrors,
					fmt.Errorf(
						"materialize mission pack receipt %s experience %s:%d: %w",
						receiptLabel,
						ref.ExperienceID,
						ref.Version,
						err,
					),
				)
				continue
			}
			if inserted {
				report.ApplicationsInserted++
			} else {
				report.ApplicationsReplayed++
			}
			if err := coordinator.repository.LinkMissionPackReceiptApplication(
				ctx,
				receipt.ReceiptID,
				ref,
				application.ApplicationID,
			); err != nil {
				materializationErrors = append(
					materializationErrors,
					fmt.Errorf(
						"link mission pack receipt %s experience %s:%d: %w",
						receiptLabel,
						ref.ExperienceID,
						ref.Version,
						err,
					),
				)
			}
		}
	}
	return report, errors.Join(materializationErrors...)
}

func deliveredMissionPackApplication(
	receipt local.MissionPackReceipt,
	ref experience.ExperienceRef,
) experience.Application {
	deliveredAt := receipt.AcceptedAt
	application := experience.Application{
		SchemaVersion:      experience.ApplicationSchemaVersion,
		Experience:         ref,
		ProjectIdentity:    receipt.ProjectIdentity,
		SessionKey:         receipt.BoundSessionKey,
		DeliveryKind:       experience.DeliveryMissionPack,
		DeliveryState:      experience.DeliveryDelivered,
		DeliveredAt:        &deliveredAt,
		OpportunityState:   experience.OpportunityUnknown,
		ApplicabilityState: experience.ApplicabilityUnknown,
		VerifierState:      experience.VerifierNotEvaluated,
		TaskOutcomeState:   experience.TaskOutcomeNotObserved,
	}
	application.ApplicationID = application.DeterministicID()
	return application
}

func validateBoundMissionPackReceiptForMaterialization(
	receipt local.MissionPackReceipt,
) error {
	if receipt.BindingState != local.ReceiptBound {
		return errors.New("receipt is not bound")
	}
	if strings.TrimSpace(receipt.ReceiptID) == "" ||
		strings.TrimSpace(receipt.PackID) == "" ||
		strings.TrimSpace(receipt.ProjectIdentity) == "" ||
		strings.TrimSpace(receipt.BoundSessionKey) == "" ||
		!receipt.Harness.Valid() ||
		receipt.Generation < 1 ||
		receipt.AcceptedAt.IsZero() ||
		receipt.ExpiresAt.IsZero() ||
		!receipt.ExpiresAt.After(receipt.AcceptedAt) ||
		len(receipt.ExperienceRefs) == 0 ||
		len(receipt.ExperienceRefs) > 3 {
		return errors.New("receipt is malformed")
	}
	seen := make(map[experience.ExperienceRef]struct{}, len(receipt.ExperienceRefs))
	for _, ref := range receipt.ExperienceRefs {
		if err := ref.Validate(); err != nil {
			return errors.New("receipt contains an invalid experience reference")
		}
		if _, duplicate := seen[ref]; duplicate {
			return errors.New("receipt contains duplicate experience references")
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func receiptDiagnosticLabel(receiptID string) string {
	if strings.TrimSpace(receiptID) == "" {
		return "<unknown>"
	}
	return fmt.Sprintf("%q", receiptID)
}
