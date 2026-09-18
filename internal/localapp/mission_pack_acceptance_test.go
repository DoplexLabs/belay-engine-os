package localapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type missionPackAcceptanceCall struct {
	packID     string
	acceptedAt time.Time
}

var _ MissionPackAcceptanceRepository = (*local.Store)(nil)

type missionPackTestAcceptanceRepository struct {
	receipt local.MissionPackReceipt
	err     error
	calls   []missionPackAcceptanceCall
}

func (r *missionPackTestAcceptanceRepository) AcceptMissionPackPreview(
	_ context.Context,
	packID string,
	acceptedAt time.Time,
) (local.MissionPackReceipt, error) {
	r.calls = append(r.calls, missionPackAcceptanceCall{
		packID:     packID,
		acceptedAt: acceptedAt,
	})
	return r.receipt, r.err
}

func TestMissionPackAcceptanceUsesOneAtomicRepositoryCall(t *testing.T) {
	now := time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC)
	packID := "mpk_" + strings.Repeat("a", 52)
	repository := &missionPackTestAcceptanceRepository{
		receipt: local.MissionPackReceipt{
			ReceiptID:    "mpr_acceptance",
			PackID:       packID,
			BindingState: local.ReceiptPending,
			ExpiresAt:    now.Add(5 * time.Minute),
		},
	}
	service, err := NewMissionPackAcceptanceService(repository)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }

	first, err := service.Accept(context.Background(), packID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Accept(context.Background(), packID)
	if err != nil {
		t.Fatal(err)
	}
	want := MissionPackAcceptanceResult{
		ReceiptID: "mpr_acceptance",
		State:     local.ReceiptPending,
		ExpiresAt: now.Add(5 * time.Minute),
	}
	if !reflect.DeepEqual(first, want) ||
		!reflect.DeepEqual(second, want) {
		t.Fatalf("acceptance results = %#v / %#v, want %#v", first, second, want)
	}
	if len(repository.calls) != 2 {
		t.Fatalf("repository calls = %#v, want two exact retries", repository.calls)
	}
	for _, call := range repository.calls {
		if call.packID != packID || !call.acceptedAt.Equal(now) {
			t.Fatalf("repository call = %#v", call)
		}
	}
}

func TestMissionPackAcceptanceRejectsInvalidIDWithoutRepositoryCall(
	t *testing.T,
) {
	repository := &missionPackTestAcceptanceRepository{}
	service, err := NewMissionPackAcceptanceService(repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Accept(context.Background(), "mpk_invalid"); err == nil {
		t.Fatal("invalid pack ID was accepted")
	}
	if len(repository.calls) != 0 {
		t.Fatalf("repository calls = %#v, want none", repository.calls)
	}
}

func TestMissionPackAcceptanceTranslatesRepositoryClassification(t *testing.T) {
	packID := "mpk_" + strings.Repeat("a", 52)
	for _, test := range []struct {
		name       string
		repository error
		want       error
	}{
		{
			name:       "not found",
			repository: local.ErrMissionPackPreviewNotFound,
			want:       ErrMissionPackPreviewNotFound,
		},
		{
			name:       "expired",
			repository: local.ErrMissionPackPreviewExpired,
			want:       ErrMissionPackPreviewExpired,
		},
		{
			name:       "conflict",
			repository: local.ErrMissionPackPreviewConflict,
			want:       ErrMissionPackPreviewConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repositoryErr := errors.New("repository operation failed")
			repository := &missionPackTestAcceptanceRepository{
				err: errors.Join(repositoryErr, test.repository),
			}
			service, err := NewMissionPackAcceptanceService(repository)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Accept(
				context.Background(),
				packID,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Accept() error = %v, want %v", err, test.want)
			}
			if errors.Is(err, test.repository) {
				t.Fatalf(
					"Accept() exposed storage classification %v",
					test.repository,
				)
			}
			if len(repository.calls) != 1 {
				t.Fatalf("repository calls = %#v, want one", repository.calls)
			}
		})
	}
}

func TestMissionPackAcceptanceWrapsUnknownRepositoryFailure(t *testing.T) {
	packID := "mpk_" + strings.Repeat("a", 52)
	repositoryErr := errors.New("repository operation failed")
	repository := &missionPackTestAcceptanceRepository{err: repositoryErr}
	service, err := NewMissionPackAcceptanceService(repository)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Accept(context.Background(), packID)
	if !errors.Is(err, repositoryErr) {
		t.Fatalf("Accept() error = %v, want wrapped repository failure", err)
	}
	for _, classified := range []error{
		ErrMissionPackPreviewNotFound,
		ErrMissionPackPreviewExpired,
		ErrMissionPackPreviewConflict,
	} {
		if errors.Is(err, classified) {
			t.Fatalf("Accept() classified unknown failure as %v", classified)
		}
	}
}
