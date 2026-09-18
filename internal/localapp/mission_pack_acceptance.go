package localapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

var (
	missionPackIDPattern          = regexp.MustCompile(`^mpk_[a-z2-7]{52}$`)
	ErrMissionPackPreviewNotFound = errors.New("Mission Pack preview not found")
	ErrMissionPackPreviewExpired  = errors.New("Mission Pack preview expired")
	ErrMissionPackPreviewConflict = errors.New("Mission Pack preview conflict")
)

type MissionPackAcceptanceRepository interface {
	AcceptMissionPackPreview(
		context.Context,
		string,
		time.Time,
	) (local.MissionPackReceipt, error)
}

type MissionPackAcceptanceResult struct {
	ReceiptID string                    `json:"receipt_id"`
	State     local.ReceiptBindingState `json:"state"`
	ExpiresAt time.Time                 `json:"expires_at"`
}

type MissionPackAcceptanceService struct {
	repository MissionPackAcceptanceRepository
	now        func() time.Time
}

func NewMissionPackAcceptanceService(
	repository MissionPackAcceptanceRepository,
) (*MissionPackAcceptanceService, error) {
	if repository == nil {
		return nil, errors.New("Mission Pack acceptance service requires a store")
	}
	return &MissionPackAcceptanceService{
		repository: repository,
		now:        time.Now,
	}, nil
}

func (s *MissionPackAcceptanceService) Accept(
	ctx context.Context,
	packID string,
) (MissionPackAcceptanceResult, error) {
	if ctx == nil {
		return MissionPackAcceptanceResult{}, errors.New(
			"Mission Pack acceptance requires context",
		)
	}
	packID = strings.TrimSpace(packID)
	if !missionPackIDPattern.MatchString(packID) {
		return MissionPackAcceptanceResult{}, errors.New(
			"Mission Pack acceptance requires a valid pack ID",
		)
	}
	receipt, err := s.repository.AcceptMissionPackPreview(
		ctx,
		packID,
		s.now().UTC(),
	)
	if err != nil {
		switch {
		case errors.Is(err, local.ErrMissionPackPreviewNotFound):
			return MissionPackAcceptanceResult{}, fmt.Errorf(
				"%w: %v",
				ErrMissionPackPreviewNotFound,
				err,
			)
		case errors.Is(err, local.ErrMissionPackPreviewExpired):
			return MissionPackAcceptanceResult{}, fmt.Errorf(
				"%w: %v",
				ErrMissionPackPreviewExpired,
				err,
			)
		case errors.Is(err, local.ErrMissionPackPreviewConflict):
			return MissionPackAcceptanceResult{}, fmt.Errorf(
				"%w: %v",
				ErrMissionPackPreviewConflict,
				err,
			)
		default:
			return MissionPackAcceptanceResult{}, fmt.Errorf(
				"accept Mission Pack preview: %w",
				err,
			)
		}
	}
	if receipt.PackID != packID ||
		strings.TrimSpace(receipt.ReceiptID) == "" ||
		receipt.ExpiresAt.IsZero() ||
		!validMissionPackReceiptState(receipt.BindingState) {
		return MissionPackAcceptanceResult{}, errors.New(
			"Mission Pack acceptance returned an invalid receipt",
		)
	}
	return MissionPackAcceptanceResult{
		ReceiptID: receipt.ReceiptID,
		State:     receipt.BindingState,
		ExpiresAt: receipt.ExpiresAt.UTC(),
	}, nil
}

func validMissionPackReceiptState(state local.ReceiptBindingState) bool {
	switch state {
	case local.ReceiptPending,
		local.ReceiptBound,
		local.ReceiptAmbiguous,
		local.ReceiptExpired,
		local.ReceiptCancelled:
		return true
	default:
		return false
	}
}
