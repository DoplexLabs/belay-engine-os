package local

import (
	"context"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestQueryActiveExperiencesFiltersBeforeLimit(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	activeCandidate := storageExperienceCandidate("active query active")
	inactiveCandidate := storageExperienceCandidate("active query inactive")
	for _, candidate := range []experience.Candidate{
		activeCandidate,
		inactiveCandidate,
	} {
		if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}

	active := storageExperience(
		activeCandidate,
		experience.LifecycleActive,
	)
	inactive := storageExperience(
		inactiveCandidate,
		experience.LifecycleSuperseded,
	)
	inactive.CreatedAt = active.CreatedAt.Add(time.Hour)
	inactive.Provenance.GeneratedAt = inactive.CreatedAt
	inactive.Governance.Approval.ApprovedAt = inactive.CreatedAt
	inactive.ContentHash = inactive.CanonicalContentHash()
	inactive.Governance.Approval.ProposedContentHash = inactive.ContentHash
	inactive.Governance.Approval.ApprovedContentHash = inactive.ContentHash
	for _, value := range []experience.Experience{active, inactive} {
		if _, err := store.InsertExperience(ctx, value); err != nil {
			t.Fatal(err)
		}
	}

	unfiltered, err := store.QueryExperiences(
		ctx,
		active.Scope.ProjectIdentity,
		1,
	)
	if err != nil ||
		len(unfiltered) != 1 ||
		unfiltered[0].Experience.ExperienceID != inactive.ExperienceID {
		t.Fatalf("unfiltered bounded query = %+v/%v", unfiltered, err)
	}
	values, err := store.QueryActiveExperiences(
		ctx,
		active.Scope.ProjectIdentity,
		1,
	)
	if err != nil ||
		len(values) != 1 ||
		values[0].Experience.ExperienceID != active.ExperienceID ||
		values[0].CurrentLifecycle != experience.LifecycleActive {
		t.Fatalf("active bounded query = %+v/%v", values, err)
	}
}
