package local

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestExperienceApprovalTokenRoundTripTamperAndExpiry(t *testing.T) {
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 17, 30, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	claims := storageExperienceApprovalClaims(now)
	token, err := store.IssueExperienceApprovalToken(claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.DecodeExperienceApprovalToken(token)
	if err != nil || got != claims {
		t.Fatalf("approval token round trip = %+v/%v", got, err)
	}

	parts := strings.SplitN(token, ".", 2)
	tampered := "A" + parts[0][1:] + "." + parts[1]
	if _, err := store.DecodeExperienceApprovalToken(tampered); !errors.Is(
		err,
		ErrExperienceApprovalTokenInvalid,
	) {
		t.Fatalf("tampered approval token error = %v", err)
	}

	store.clock = func() time.Time {
		return claims.ExpiresAt.Add(time.Nanosecond)
	}
	if _, err := store.DecodeExperienceApprovalToken(token); !errors.Is(
		err,
		ErrExperienceApprovalTokenExpired,
	) {
		t.Fatalf("expired approval token error = %v", err)
	}
}

func storageExperienceApprovalClaims(
	now time.Time,
) experience.ApprovalTokenClaims {
	return experience.ApprovalTokenClaims{
		Version:             experience.ApprovalTokenVersion,
		CandidateID:         "exc_candidate",
		ProposalID:          "exs_proposal",
		ProjectIdentity:     "git@example.test:doplexlabs/belay.git",
		SemanticInputHash:   storageSHA256("semantic input"),
		ProposedContentHash: storageSHA256("proposed content"),
		EvidenceGeneration:  storageSHA256("evidence generation"),
		IssuedAt:            now,
		ExpiresAt:           now.Add(experienceApprovalTokenLifetime),
	}
}
