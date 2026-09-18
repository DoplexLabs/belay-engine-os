package local

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestCompileActiveExperienceGenerationFiltersAndIsDeterministic(t *testing.T) {
	ctx := context.Background()
	compiledAt := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	otherProject := "git@example.test:doplexlabs/other.git"

	activeFirst := generationExperience(
		t,
		"generation active first",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(time.Hour),
	)
	activeSecond := generationExperience(
		t,
		"generation active second",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(time.Hour),
	)
	paused := generationExperience(
		t,
		"generation paused",
		storageProjectIdentity,
		experience.LifecyclePaused,
		compiledAt.Add(time.Hour),
	)
	expiredAtBoundary := generationExperience(
		t,
		"generation expired at boundary",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt,
	)
	crossProject := generationExperience(
		t,
		"generation cross project",
		otherProject,
		experience.LifecycleActive,
		compiledAt.Add(time.Hour),
	)

	firstStore := openStorageTestStore(t)
	for _, value := range []experience.Experience{
		activeSecond,
		paused,
		crossProject,
		expiredAtBoundary,
		activeFirst,
	} {
		insertGenerationExperience(t, firstStore, value)
	}
	first, err := firstStore.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantRefs := sortedGenerationRefs(activeFirst, activeSecond)
	if first.Replayed ||
		!reflect.DeepEqual(first.Generation.ExperienceRefs, wantRefs) {
		t.Fatalf("first compilation = %+v, want refs %+v", first, wantRefs)
	}

	replayed, err := firstStore.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed ||
		replayed.Generation.Generation != first.Generation.Generation ||
		replayed.Generation.CompiledHash != first.Generation.CompiledHash ||
		!reflect.DeepEqual(
			replayed.Generation.ExperienceRefs,
			first.Generation.ExperienceRefs,
		) {
		t.Fatalf("replayed compilation = %+v, first %+v", replayed, first)
	}
	var rows int
	if err := firstStore.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM experience_generations
		WHERE project_identity = ?`,
		storageProjectIdentity,
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("generation rows after replay = %d, want 1", rows)
	}

	secondStore := openStorageTestStore(t)
	for _, value := range []experience.Experience{
		activeFirst,
		expiredAtBoundary,
		crossProject,
		paused,
		activeSecond,
	} {
		insertGenerationExperience(t, secondStore, value)
	}
	shuffled, err := secondStore.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if shuffled.Generation.CompiledHash != first.Generation.CompiledHash ||
		!reflect.DeepEqual(
			shuffled.Generation.ExperienceRefs,
			first.Generation.ExperienceRefs,
		) {
		t.Fatalf(
			"shuffled compilation = %+v, first %+v",
			shuffled.Generation,
			first.Generation,
		)
	}
}

func TestCompileActiveExperienceGenerationChangeRollbackAndEmpty(t *testing.T) {
	ctx := context.Background()
	compiledAt := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	store := openStorageTestStore(t)

	firstValue := generationExperience(
		t,
		"generation rollback first",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(time.Hour),
	)
	insertGenerationExperience(t, store, firstValue)
	first, err := store.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt,
	)
	if err != nil {
		t.Fatal(err)
	}

	secondValue := generationExperience(
		t,
		"generation rollback second",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(time.Hour),
	)
	insertGenerationExperience(t, store, secondValue)
	second, err := store.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Replayed || second.Generation.Generation != 2 ||
		second.Generation.PreviousGeneration == nil ||
		*second.Generation.PreviousGeneration != 1 ||
		!reflect.DeepEqual(
			second.Generation.ExperienceRefs,
			sortedGenerationRefs(firstValue, secondValue),
		) {
		t.Fatalf("second compilation = %+v", second)
	}

	rolledBack, err := store.RollbackExperienceGeneration(
		ctx,
		storageProjectIdentity,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Generation != first.Generation.Generation ||
		!reflect.DeepEqual(
			rolledBack.ExperienceRefs,
			first.Generation.ExperienceRefs,
		) {
		t.Fatalf("rollback = %+v, first %+v", rolledBack, first.Generation)
	}

	pauseGenerationExperience(
		t,
		store,
		firstValue,
		compiledAt.Add(2*time.Minute),
	)
	pauseGenerationExperience(
		t,
		store,
		secondValue,
		compiledAt.Add(2*time.Minute),
	)
	empty, err := store.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Replayed || empty.Generation.Generation != 3 ||
		empty.Generation.ExperienceRefs == nil ||
		len(empty.Generation.ExperienceRefs) != 0 {
		t.Fatalf("empty compilation = %+v", empty)
	}
	replayedEmpty, err := store.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replayedEmpty.Replayed ||
		replayedEmpty.Generation.Generation != empty.Generation.Generation ||
		replayedEmpty.Generation.CompiledHash != empty.Generation.CompiledHash {
		t.Fatalf("replayed empty compilation = %+v", replayedEmpty)
	}
}

func TestExperienceGenerationScanRejectsMalformedMembership(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(ExperienceGeneration) ExperienceGeneration
		wantError string
	}{
		{
			name: "invalid ref",
			mutate: func(value ExperienceGeneration) ExperienceGeneration {
				value.ExperienceRefs[0].Version = 0
				return value
			},
			wantError: "invalid persisted experience generation",
		},
		{
			name: "duplicate refs",
			mutate: func(value ExperienceGeneration) ExperienceGeneration {
				value.ExperienceRefs[1] = value.ExperienceRefs[0]
				return value
			},
			wantError: "invalid persisted experience generation",
		},
		{
			name: "unsorted refs",
			mutate: func(value ExperienceGeneration) ExperienceGeneration {
				value.ExperienceRefs[0], value.ExperienceRefs[1] =
					value.ExperienceRefs[1], value.ExperienceRefs[0]
				return value
			},
			wantError: "invalid persisted experience generation",
		},
		{
			name: "payload index mismatch",
			mutate: func(value ExperienceGeneration) ExperienceGeneration {
				value.CompiledHash = storageSHA256("different compiled hash")
				return value
			},
			wantError: "index does not match payload",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := openStorageTestStore(t)
			generation := ExperienceGeneration{
				ProjectIdentity: storageProjectIdentity,
				Generation:      1,
				CompiledHash:    storageSHA256("valid generation"),
				ExperienceRefs: []experience.ExperienceRef{
					{ExperienceID: "exp_generation_a", Version: 1},
					{ExperienceID: "exp_generation_b", Version: 1},
				},
				State:       GenerationActive,
				CompiledAt:  time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC),
				ActivatedAt: time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC),
			}
			if _, err := store.ActivateExperienceGeneration(
				ctx,
				generation,
			); err != nil {
				t.Fatal(err)
			}
			malformed := test.mutate(generation)
			_, payload, err := store.sealDomainJSON(
				"experience_generation",
				generationRecordID(
					generation.ProjectIdentity,
					generation.Generation,
				),
				"payload",
				malformed,
				maxGenerationPayloadBytes,
			)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = withMutationTx(
				ctx,
				tx,
				mutationExperienceGeneration,
				func() error {
					_, err := tx.ExecContext(ctx, `
						UPDATE experience_generations
						SET payload = ?
						WHERE project_identity = ? AND generation = ?`,
						payload,
						generation.ProjectIdentity,
						generation.Generation,
					)
					return err
				},
			)
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.GetActiveExperienceGeneration(
				ctx,
				generation.ProjectIdentity,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("scan error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestExperienceGenerationLegacyEmptyMembershipRemainsReadable(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	legacy := ExperienceGeneration{
		ProjectIdentity: storageProjectIdentity,
		Generation:      1,
		CompiledHash:    storageSHA256("legacy empty generation"),
		State:           GenerationActive,
		CompiledAt:      time.Date(2026, 9, 10, 20, 30, 0, 0, time.UTC),
		ActivatedAt:     time.Date(2026, 9, 10, 20, 30, 0, 0, time.UTC),
	}
	activated, err := store.ActivateExperienceGeneration(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetActiveExperienceGeneration(
		ctx,
		legacy.ProjectIdentity,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(activated, legacy) ||
		!reflect.DeepEqual(persisted, legacy) {
		t.Fatalf(
			"legacy empty generation = activated %+v, persisted %+v",
			activated,
			persisted,
		)
	}
}

func TestMissionPackReceiptRequiresExactGenerationMembership(t *testing.T) {
	ctx := context.Background()
	compiledAt := time.Date(2026, 9, 10, 21, 0, 0, 0, time.UTC)
	store := openStorageTestStore(t)

	member := generationExperience(
		t,
		"receipt generation member",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(time.Hour),
	)
	insertGenerationExperience(t, store, member)
	generation, err := store.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	nonmember := generationExperience(
		t,
		"receipt active nonmember",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(time.Hour),
	)
	insertGenerationExperience(t, store, nonmember)

	memberReceipt := storageMissionPackReceipt(member)
	memberReceipt.ReceiptID = "mpr_generation_member"
	memberReceipt.Generation = generation.Generation.Generation
	memberReceipt.AcceptedAt = compiledAt.Add(time.Minute)
	memberReceipt.ExpiresAt = memberReceipt.AcceptedAt.Add(5 * time.Minute)
	if inserted, err := store.RecordPendingMissionPackReceipt(
		ctx,
		memberReceipt,
	); err != nil || !inserted {
		t.Fatalf("member receipt = %v, %v", inserted, err)
	}

	nonmemberReceipt := storageMissionPackReceipt(nonmember)
	nonmemberReceipt.ReceiptID = "mpr_generation_nonmember"
	nonmemberReceipt.Generation = generation.Generation.Generation
	nonmemberReceipt.AcceptedAt = compiledAt.Add(time.Minute)
	nonmemberReceipt.ExpiresAt = nonmemberReceipt.AcceptedAt.Add(5 * time.Minute)
	if _, err := store.RecordPendingMissionPackReceipt(
		ctx,
		nonmemberReceipt,
	); err == nil ||
		!strings.Contains(err.Error(), "not a member of generation") {
		t.Fatalf("active nonmember receipt error = %v", err)
	}
}

func TestExperienceGenerationValidationRejectsSafetyLimit(t *testing.T) {
	refs := make(
		[]experience.ExperienceRef,
		maxCompiledExperienceRefs+1,
	)
	for index := range refs {
		refs[index] = experience.ExperienceRef{
			ExperienceID: fmt.Sprintf(
				"exp_generation_limit_%03d",
				index,
			),
			Version: 1,
		}
	}
	if err := validateGenerationExperienceRefs(refs); !errors.Is(
		err,
		ErrExperienceGenerationLimit,
	) {
		t.Fatalf("generation safety-limit error = %v", err)
	}
}

func generationExperience(
	t *testing.T,
	canary string,
	projectIdentity string,
	state experience.LifecycleState,
	expiresAt time.Time,
) experience.Experience {
	t.Helper()
	candidate := storageExperienceCandidate(canary)
	candidate.ProjectIdentity = projectIdentity
	candidate.Proposal.Scope.ProjectIdentity = projectIdentity
	candidate.Proposal.Applicability.ExpiresAt = timePointer(expiresAt)
	candidate.CandidateID = candidate.DeterministicID()
	return storageExperience(candidate, state)
}

func insertGenerationExperience(
	t *testing.T,
	store *Store,
	value experience.Experience,
) {
	t.Helper()
	candidate := storageExperienceCandidate(
		value.Evidence.Refs[0].Excerpt,
	)
	candidate.ProjectIdentity = value.Scope.ProjectIdentity
	candidate.Proposal.Scope.ProjectIdentity = value.Scope.ProjectIdentity
	candidate.Proposal.Applicability = value.Applicability
	candidate.CandidateID = candidate.DeterministicID()
	if candidate.CandidateID != value.OriginCandidateID {
		t.Fatalf(
			"generation fixture candidate = %s, experience origin %s",
			candidate.CandidateID,
			value.OriginCandidateID,
		)
	}
	if _, err := store.InsertExperienceCandidate(
		context.Background(),
		candidate,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertExperience(
		context.Background(),
		value,
	); err != nil {
		t.Fatal(err)
	}
}

func sortedGenerationRefs(
	values ...experience.Experience,
) []experience.ExperienceRef {
	refs := make([]experience.ExperienceRef, len(values))
	for index, value := range values {
		refs[index] = experience.ExperienceRef{
			ExperienceID: value.ExperienceID,
			Version:      value.Version,
		}
	}
	for first := 0; first < len(refs); first++ {
		for second := first + 1; second < len(refs); second++ {
			left := refs[first]
			right := refs[second]
			if left.ExperienceID > right.ExperienceID ||
				(left.ExperienceID == right.ExperienceID &&
					left.Version > right.Version) {
				refs[first], refs[second] = refs[second], refs[first]
			}
		}
	}
	return refs
}

func pauseGenerationExperience(
	t *testing.T,
	store *Store,
	value experience.Experience,
	occurredAt time.Time,
) {
	t.Helper()
	transition := experience.LifecycleTransition{
		Experience: experience.ExperienceRef{
			ExperienceID: value.ExperienceID,
			Version:      value.Version,
		},
		FromState:  experience.LifecycleActive,
		ToState:    experience.LifecyclePaused,
		ReasonCode: "user_paused",
		ActorKind:  experience.ActorUser,
		ActorID:    "local_user",
		OccurredAt: occurredAt,
	}
	transition.TransitionID = transition.DeterministicID()
	if inserted, err := store.AppendExperienceTransition(
		context.Background(),
		transition,
	); err != nil || !inserted {
		t.Fatalf("pause transition = %v, %v", inserted, err)
	}
}
