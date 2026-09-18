package localapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	trajectoryderive "github.com/DoplexLabs/belay-engine/internal/trajectory/derive"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	maxTrajectoryTranscriptTurns = 10000
	maxTrajectoryCanonicalEvents = 10000
	trajectoryEventPageSize      = 500
	defaultTrajectoryBatchLimit  = 25
)

type TrajectoryCanonicalEventStore interface {
	QuerySessionTimeline(context.Context, model.TimelineQuery) (model.EventPage, error)
}

type TrajectoryDerivationStore interface {
	TrajectoryCanonicalEventStore
	GetTranscriptSession(context.Context, string) (transcript.Session, error)
	QueryTranscriptTurns(context.Context, string, int) ([]transcript.Turn, error)
	InsertTrajectoryEdge(context.Context, trajectory.Edge) (bool, error)
	InsertOutcome(context.Context, trajectory.Outcome) (bool, error)
}

type TrajectoryDerivationReport struct {
	SessionKey        string
	DerivationVersion string
	EdgesInserted     int
	EdgesReplayed     int
	OutcomesInserted  int
	OutcomesReplayed  int
	Coverage          trajectoryderive.Coverage
	Diagnostics       []trajectoryderive.Diagnostic
}

type IncrementalTrajectoryDerivationStore interface {
	TrajectoryDerivationStore
	ListDirtyTrajectorySessions(
		context.Context,
		string,
		int,
	) ([]local.TrajectoryDerivationClaim, error)
	MarkTrajectoryDerivationCurrent(
		context.Context,
		local.TrajectoryDerivationClaim,
		trajectoryderive.Coverage,
		[]trajectoryderive.Diagnostic,
	) (local.TrajectoryDerivationState, error)
	RecordTrajectoryDerivationFailure(
		context.Context,
		local.TrajectoryDerivationClaim,
		string,
		bool,
	) (local.TrajectoryDerivationState, error)
}

type TrajectoryBatchReport struct {
	Dirty    int
	Complete int
	Partial  int
	Failed   int
	Stale    int
}

func DeriveSessionTrajectoryOnce(
	ctx context.Context,
	store TrajectoryDerivationStore,
	sessionKey string,
) (TrajectoryDerivationReport, error) {
	if store == nil || sessionKey == "" {
		return TrajectoryDerivationReport{}, errors.New(
			"trajectory derivation requires a store and session",
		)
	}
	session, err := store.GetTranscriptSession(ctx, sessionKey)
	if err != nil {
		return TrajectoryDerivationReport{}, err
	}
	turns, err := store.QueryTranscriptTurns(
		ctx,
		sessionKey,
		maxTrajectoryTranscriptTurns,
	)
	if err != nil {
		return TrajectoryDerivationReport{}, err
	}
	events, canonicalComplete, err := loadTrajectoryCanonicalEvents(
		ctx,
		store,
		sessionKey,
	)
	if err != nil {
		return TrajectoryDerivationReport{}, err
	}
	derived, err := trajectoryderive.Session(trajectoryderive.Input{
		Session:                 session,
		Turns:                   turns,
		CanonicalEvents:         events,
		ProjectConfig:           loadProjectConfig(session.ProjectPath),
		TranscriptTurnsComplete: len(turns) >= session.TurnCount,
		CanonicalEventsComplete: canonicalComplete,
	})
	if err != nil {
		return TrajectoryDerivationReport{}, err
	}
	report := TrajectoryDerivationReport{
		SessionKey:        sessionKey,
		DerivationVersion: derived.DerivationVersion,
		Coverage:          derived.Coverage,
		Diagnostics:       append([]trajectoryderive.Diagnostic(nil), derived.Diagnostics...),
	}
	for _, edge := range derived.Edges {
		inserted, err := store.InsertTrajectoryEdge(ctx, edge)
		if err != nil {
			return report, err
		}
		if inserted {
			report.EdgesInserted++
		} else {
			report.EdgesReplayed++
		}
	}
	for _, outcome := range derived.Outcomes {
		inserted, err := store.InsertOutcome(ctx, outcome)
		if err != nil {
			return report, err
		}
		if inserted {
			report.OutcomesInserted++
		} else {
			report.OutcomesReplayed++
		}
	}
	return report, nil
}

func AnalyzeTrajectorySessionsOnce(
	ctx context.Context,
	store IncrementalTrajectoryDerivationStore,
	limit int,
) (TrajectoryBatchReport, error) {
	if store == nil {
		return TrajectoryBatchReport{}, errors.New(
			"incremental trajectory derivation requires a store",
		)
	}
	if limit <= 0 {
		limit = defaultTrajectoryBatchLimit
	}
	claims, err := store.ListDirtyTrajectorySessions(
		ctx,
		trajectoryderive.Version,
		limit,
	)
	if err != nil {
		return TrajectoryBatchReport{}, err
	}
	report := TrajectoryBatchReport{Dirty: len(claims)}
	var runErrors []error
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(runErrors, err)...)
		}
		derived, err := DeriveSessionTrajectoryOnce(
			ctx,
			store,
			claim.SessionKey,
		)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return report, errors.Join(append(runErrors, context.Canceled)...)
			}
			_, recordErr := store.RecordTrajectoryDerivationFailure(
				ctx,
				claim,
				"trajectory_derivation_failed",
				true,
			)
			switch {
			case errors.Is(recordErr, local.ErrTrajectoryDerivationStale):
				report.Stale++
			case recordErr != nil:
				runErrors = append(
					runErrors,
					fmt.Errorf(
						"record failed trajectory session %q: %w",
						claim.SessionKey,
						recordErr,
					),
				)
			default:
				report.Failed++
			}
			runErrors = append(
				runErrors,
				fmt.Errorf(
					"derive trajectory session %q: %w",
					claim.SessionKey,
					err,
				),
			)
			continue
		}
		_, err = store.MarkTrajectoryDerivationCurrent(
			ctx,
			claim,
			derived.Coverage,
			derived.Diagnostics,
		)
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return report, errors.Join(append(runErrors, context.Canceled)...)
		}
		switch {
		case errors.Is(err, local.ErrTrajectoryDerivationStale):
			report.Stale++
		case err != nil:
			runErrors = append(
				runErrors,
				fmt.Errorf(
					"publish trajectory session %q: %w",
					claim.SessionKey,
					err,
				),
			)
		case derived.Coverage.FullyDerived:
			report.Complete++
		default:
			report.Partial++
		}
	}
	return report, errors.Join(runErrors...)
}

func PollTrajectoryDerivation(
	ctx context.Context,
	store IncrementalTrajectoryDerivationStore,
	interval time.Duration,
	onError func(error),
) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	run := func() {
		_, err := AnalyzeTrajectorySessionsOnce(
			ctx,
			store,
			defaultTrajectoryBatchLimit,
		)
		if err != nil &&
			!errors.Is(err, context.Canceled) &&
			ctx.Err() == nil &&
			onError != nil {
			onError(err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func loadTrajectoryCanonicalEvents(
	ctx context.Context,
	store TrajectoryCanonicalEventStore,
	sessionKey string,
) ([]model.Event, bool, error) {
	events := make([]model.Event, 0)
	var snapshot int64
	var cursor *model.EventPosition
	for len(events) < maxTrajectoryCanonicalEvents {
		limit := trajectoryEventPageSize
		if remaining := maxTrajectoryCanonicalEvents - len(events); remaining < limit {
			limit = remaining
		}
		page, err := store.QuerySessionTimeline(ctx, model.TimelineQuery{
			SessionID: sessionKey,
			Limit:     limit,
			Snapshot:  snapshot,
			Cursor:    cursor,
		})
		if err != nil {
			return nil, false, err
		}
		if snapshot == 0 {
			snapshot = page.Snapshot
		}
		events = append(events, page.Data...)
		if len(page.Data) < limit {
			return events, true, nil
		}
		last := page.Data[len(page.Data)-1]
		cursor = &model.EventPosition{
			OccurredAt:     last.OccurredAt,
			SourceSequence: last.Source.Sequence,
			EventID:        last.EventID,
		}
	}
	lookAhead, err := store.QuerySessionTimeline(ctx, model.TimelineQuery{
		SessionID: sessionKey,
		Limit:     1,
		Snapshot:  snapshot,
		Cursor:    cursor,
	})
	if err != nil {
		return nil, false, err
	}
	return events, len(lookAhead.Data) == 0, nil
}
