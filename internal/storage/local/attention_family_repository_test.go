package local

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
)

func TestAttentionFamiliesGroupReviewedNumbatSignalsAndKeepExactBelayIssues(t *testing.T) {
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	first := addAttentionFamilyOccurrence(
		t, store, "numbat-a", "claude-code", "finding-a", "1.1", base, "low",
	)
	second := addAttentionFamilyOccurrence(
		t, store, "numbat-b", "codex", "finding-b", "1.1", base.Add(time.Minute), "high",
	)
	_ = addAttentionFamilyOccurrence(
		t, store, "unsupported", "codex", "finding-c", "1.2", base.Add(2*time.Minute), "high",
	)
	exact := addBelayAttentionFamilyOccurrence(
		t, store, "belay-a", "codex", base.Add(3*time.Minute),
	)

	page, err := store.QueryAttentionFamilies(context.Background(), model.AttentionFamilyQuery{
		Filter: model.AttentionFamilyFilter{
			AttentionKind: model.AttentionKindIssue,
			Experimental:  model.ExperimentalStable,
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 2 || page.HasMore || page.CursorEpoch == "" ||
		page.Snapshot <= 0 || page.RetentionGeneration < 1 {
		t.Fatalf("page = %+v", page)
	}
	var mapped, native model.AttentionFamilySummary
	for _, family := range page.Data {
		switch family.Kind {
		case model.AttentionFamilyKindMappedUpstream:
			mapped = family
		case model.AttentionFamilyKindExactIssue:
			native = family
		}
	}
	if mapped.FamilyID == "" ||
		mapped.MappingKey != sourcecatalog.GuardrailsConfigurationMappingKey ||
		mapped.MappingVersion != "1" ||
		mapped.GroupingVersion != "1" ||
		mapped.Severity != "low" ||
		mapped.SupportingIssueCount != 2 ||
		mapped.OccurrenceCount != 2 ||
		mapped.SessionCount != 2 ||
		len(mapped.Harnesses) != 2 ||
		mapped.Scope.Unscoped != 2 ||
		mapped.RepresentativeIssueID != second.IssueID {
		t.Fatalf("mapped family = %+v first=%+v second=%+v", mapped, first, second)
	}
	if native.RepresentativeIssueID != exact.IssueID ||
		native.SupportingIssueCount != 1 ||
		native.OccurrenceCount != 1 ||
		native.SessionCount != 1 {
		t.Fatalf("exact family = %+v exact=%+v", native, exact)
	}

	members, err := store.QueryAttentionFamilyMembers(
		context.Background(),
		model.AttentionFamilyMemberQuery{
			FamilyID: mapped.FamilyID,
			GroupKey: mapped.GroupKey,
			Filter: model.AttentionFamilyFilter{
				AttentionKind: model.AttentionKindIssue,
				Experimental:  model.ExperimentalStable,
			},
			Limit:               1,
			CursorEpoch:         page.CursorEpoch,
			Snapshot:            page.Snapshot,
			RetentionGeneration: page.RetentionGeneration,
			IssuedAt:            page.IssuedAt,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !members.Found || len(members.Data) != 1 || !members.HasMore ||
		members.Data[0].IssueID != second.IssueID {
		t.Fatalf("first member page = %+v", members)
	}
	next, err := store.QueryAttentionFamilyMembers(
		context.Background(),
		model.AttentionFamilyMemberQuery{
			FamilyID: mapped.FamilyID,
			GroupKey: mapped.GroupKey,
			Filter: model.AttentionFamilyFilter{
				AttentionKind: model.AttentionKindIssue,
				Experimental:  model.ExperimentalStable,
			},
			Limit:               1,
			CursorEpoch:         page.CursorEpoch,
			Snapshot:            page.Snapshot,
			RetentionGeneration: page.RetentionGeneration,
			IssuedAt:            page.IssuedAt,
			Cursor: &model.AttentionFamilyMemberPosition{
				AnalysisStatusRank: analysisStatusRank(members.Data[0].AnalysisStatus),
				LastObserved:       members.Data[0].LastObservedAt,
				IssueID:            members.Data[0].IssueID,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Found || len(next.Data) != 1 || next.HasMore ||
		next.Data[0].IssueID != first.IssueID {
		t.Fatalf("second member page = %+v", next)
	}
}

func TestAttentionFamilyMemberUsesLatestMatchingSessionContext(t *testing.T) {
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	first := addAttentionFamilyOccurrence(
		t, store, "shared-old", "claude-code", "finding-shared-old", "1.1",
		base, "low",
	)
	latestAt := base.Add(2 * time.Hour)
	addAttentionFamilyOccurrenceWithIdentity(
		t,
		store,
		"shared-latest",
		"codex",
		"finding-shared-latest",
		"1.1",
		latestAt,
		"low",
		first.FingerprintID,
		first.IssueID,
	)

	page, err := store.QueryAttentionFamilies(context.Background(), model.AttentionFamilyQuery{
		Filter: model.AttentionFamilyFilter{
			AttentionKind: model.AttentionKindIssue,
			Experimental:  model.ExperimentalStable,
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 ||
		page.Data[0].SupportingIssueCount != 1 ||
		page.Data[0].OccurrenceCount != 2 ||
		page.Data[0].SessionCount != 2 {
		t.Fatalf("family page = %+v", page)
	}
	members, err := store.QueryAttentionFamilyMembers(
		context.Background(),
		model.AttentionFamilyMemberQuery{
			FamilyID: page.Data[0].FamilyID,
			GroupKey: page.Data[0].GroupKey,
			Filter: model.AttentionFamilyFilter{
				AttentionKind: model.AttentionKindIssue,
				Experimental:  model.ExperimentalStable,
			},
			Limit:               20,
			CursorEpoch:         page.CursorEpoch,
			Snapshot:            page.Snapshot,
			RetentionGeneration: page.RetentionGeneration,
			IssuedAt:            page.IssuedAt,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(members.Data) != 1 {
		t.Fatalf("members = %+v", members)
	}
	member := members.Data[0]
	if member.IssueID != first.IssueID ||
		member.SessionCount != 2 ||
		member.SessionKey != "shared-latest" ||
		member.SessionSelection != model.AttentionFamilyMemberSessionSelectionLatest ||
		member.SessionStartedAt == nil ||
		!member.SessionStartedAt.Equal(latestAt) ||
		member.SessionLastActiveAt == nil ||
		!member.SessionLastActiveAt.Equal(latestAt) ||
		member.CitedEventCount != 1 ||
		member.EvidenceFirstAt == nil ||
		!member.EvidenceFirstAt.Equal(latestAt) ||
		member.EvidenceLastAt == nil ||
		!member.EvidenceLastAt.Equal(latestAt) {
		t.Fatalf("latest member context = %+v", member)
	}
}

func TestAttentionFamilyFiltersRecomputeVisibleCountsAndUseMappedSeverity(t *testing.T) {
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	_ = addAttentionFamilyOccurrence(
		t, store, "old-claude", "claude-code", "finding-old", "1.1", base, "critical",
	)
	recent := addAttentionFamilyOccurrence(
		t, store, "recent-codex", "codex", "finding-recent", "1.1",
		base.Add(time.Hour), "critical",
	)

	after := base.Add(30 * time.Minute)
	page, err := store.QueryAttentionFamilies(context.Background(), model.AttentionFamilyQuery{
		Filter: model.AttentionFamilyFilter{
			Severity:      "low",
			Harness:       "CODEX",
			ObservedAfter: &after,
			AttentionKind: model.AttentionKindIssue,
			Experimental:  model.ExperimentalStable,
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("filtered families = %+v", page.Data)
	}
	family := page.Data[0]
	if family.Severity != "low" ||
		family.SupportingIssueCount != 1 ||
		family.OccurrenceCount != 1 ||
		family.SessionCount != 1 ||
		family.RepresentativeIssueID != recent.IssueID ||
		len(family.Harnesses) != 1 ||
		family.Harnesses[0] != "codex" {
		t.Fatalf("filtered family = %+v", family)
	}
	page, err = store.QueryAttentionFamilies(context.Background(), model.AttentionFamilyQuery{
		Filter: model.AttentionFamilyFilter{
			Severity:      "critical",
			AttentionKind: model.AttentionKindIssue,
			Experimental:  model.ExperimentalStable,
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("source severity admitted mapped family: %+v", page.Data)
	}
}

func TestAttentionFamilyScale25000SummariesReturnsOnlyBoundedSQLPage(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	observedAt := formatProjectionTime(
		time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC),
	)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_projection_metadata
			SET current_generation = 1
			WHERE singleton = 1;
			UPDATE issue_summary_metadata
			SET readiness = 'ready', build_generation = 1,
				materialized_generation = 1
			WHERE singleton = 1;
			WITH RECURSIVE seq(n) AS (
				VALUES(1)
				UNION ALL SELECT n + 1 FROM seq WHERE n < 25000
			)
			INSERT INTO issue_summary_revisions (
				summary_revision_id, issue_id, fingerprint_id,
				fingerprint_version, origin, detector_id, detector_version,
				category, title_code, severity, severity_rank, confidence,
				scope_quality, first_observed_at, last_observed_at,
				occurrence_count, session_count, repeated, analysis_status,
				evidence_complete, retained_history_only, experimental,
				visible_from_generation, created_at
			)
			SELECT
				printf('family-scale-revision-%05d', n),
				printf('family-scale-issue-%05d', n),
				printf('family-scale-fingerprint-%05d', n),
				'1', 'belay', 'explicit_command_failure', '1',
				'command_failure', 'issue.explicit_command_failure',
				'medium', 3, 'high', 'resolved', ?, ?,
				1, 1, 0, 'current', 1, 0, 0, 1, ?
			FROM seq;
			WITH RECURSIVE seq(n) AS (
				VALUES(1)
				UNION ALL SELECT n + 1 FROM seq WHERE n < 25000
			)
			INSERT INTO issue_occurrences (
				revision_id, occurrence_id, issue_id, fingerprint_id,
				fingerprint_version, origin, session_key, harness,
				detector_id, detector_version, projection_version, category,
				title_code, severity, confidence, scope_quality,
				first_observed_at, last_observed_at, evidence_complete,
				retained_history_only, experimental, analysis_status,
				analysis_generation, evidence_payload, evidence_encoding,
				visible_from_generation, created_at, updated_at
			)
			SELECT
				printf('family-scale-occ-revision-%05d', n),
				printf('family-scale-occurrence-%05d', n),
				printf('family-scale-issue-%05d', n),
				printf('family-scale-fingerprint-%05d', n),
				'1', 'belay', printf('family-scale-session-%05d', n), 'codex',
				'explicit_command_failure', '1', '1', 'command_failure',
				'issue.explicit_command_failure', 'medium', 'high', 'resolved',
				?, ?, 1, 0, 0, 'current', 1, X'00', 'test', 1, ?, ?
			FROM seq`,
			observedAt, observedAt, observedAt,
			observedAt, observedAt, observedAt, observedAt,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	page, err := store.QueryAttentionFamilies(ctx, model.AttentionFamilyQuery{
		Filter: model.AttentionFamilyFilter{
			AttentionKind: model.AttentionKindIssue,
			Experimental:  model.ExperimentalStable,
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 20 || !page.HasMore {
		t.Fatalf("scale family page = returned %d has_more=%v", len(page.Data), page.HasMore)
	}
	firstPageIDs := make(map[string]struct{}, len(page.Data))
	for _, family := range page.Data {
		firstPageIDs[family.FamilyID] = struct{}{}
	}
	position := page.Data[len(page.Data)-1]
	next, err := store.QueryAttentionFamilies(ctx, model.AttentionFamilyQuery{
		Filter: model.AttentionFamilyFilter{
			AttentionKind: model.AttentionKindIssue,
			Experimental:  model.ExperimentalStable,
		},
		Limit:               20,
		CursorEpoch:         page.CursorEpoch,
		Snapshot:            page.Snapshot,
		RetentionGeneration: page.RetentionGeneration,
		IssuedAt:            page.IssuedAt,
		Cursor: &model.AttentionFamilyPosition{
			SeverityRank:         familySeverityRank(position.Severity),
			SupportingIssueCount: position.SupportingIssueCount,
			LastObserved:         position.LastObservedAt,
			GroupKey:             position.GroupKey,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Data) != 20 || !next.HasMore {
		t.Fatalf("scale continuation = %+v", next)
	}
	for _, family := range next.Data {
		if _, duplicate := firstPageIDs[family.FamilyID]; duplicate {
			t.Fatalf("scale continuation repeated first-page family %q", family.FamilyID)
		}
	}

	cte, args, err := attentionFamilyCTE(1, model.AttentionFamilyFilter{
		AttentionKind: model.AttentionKindIssue,
		Experimental:  model.ExperimentalStable,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cte, "JOIN events") {
		t.Fatalf("family-list CTE must not enrich session events before paging:\n%s", cte)
	}
	args = append(args, 21)
	rows, err := store.db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+cte+`
		SELECT `+attentionFamilySummaryColumns("fr", "representative")+`
		FROM family_rollup fr
		JOIN ranked_members representative
			ON representative.group_key = fr.group_key
			AND representative.family_member_rank = 1
		ORDER BY fr.severity_rank DESC, fr.supporting_issue_count DESC,
			fr.last_observed_at DESC, fr.group_key ASC
		LIMIT ?`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "issue_summary") ||
		!strings.Contains(plan.String(), "issue_occurrences") {
		t.Fatalf("family query plan does not use projection tables:\n%s", plan.String())
	}
}

func addAttentionFamilyOccurrenceWithIdentity(
	t *testing.T,
	store *Store,
	sessionID string,
	harness string,
	findingID string,
	rawRuleVersion string,
	observedAt time.Time,
	sourceSeverity string,
	fingerprintID string,
	issueID string,
) model.IssueOccurrence {
	t.Helper()
	ctx := context.Background()
	event := storageTestEvent(
		fmt.Sprintf(
			"00000000-0000-7000-8000-%012x",
			uint64(observedAt.UnixNano())&0xffffffffffff,
		),
		sessionID,
		1,
		observedAt,
	)
	event.Source.Agent = harness
	event.Source.RunID = "run-" + sessionID
	event.Observation.Type = "config.agent"
	appendResult, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordFindingResolved(ctx, Finding{
		FindingID:     findingID,
		SourceRunID:   event.Source.RunID,
		SessionKey:    sessionID,
		DetectedAt:    observedAt,
		RuleID:        sourcecatalog.GuardrailsSourceSignalCode,
		RuleVersion:   rawRuleVersion,
		Severity:      sourceSeverity,
		SourceAgent:   harness,
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	}); err != nil {
		t.Fatal(err)
	}
	code := sourcecatalog.GuardrailsSourceSignalCode
	occurrence := testIssueOccurrence(
		sessionID,
		harness,
		event.EventID,
		fingerprintID,
		issueID,
		sourceSeverity,
		"high",
		observedAt,
		model.ScopeUnscoped,
	)
	occurrence.OccurrenceID = "occ-" + sessionID
	occurrence.Origin = "numbat"
	occurrence.OriginRecordID = findingID
	occurrence.FingerprintVersion = "1"
	occurrence.Provenance = model.DetectorProvenance{
		DetectorID:         "numbat_finding",
		DetectorVersion:    sourcecatalog.OpaqueRuleVersion(rawRuleVersion),
		FingerprintVersion: "1",
		ProjectionVersion:  "1",
	}
	occurrence.Category = "numbat_finding"
	occurrence.TitleCode = "issue.numbat_finding"
	occurrence.SourceSignalCode = &code
	occurrence.Severity = sourceSeverity
	occurrence.Confidence = "high"
	occurrence.ScopeQuality = model.ScopeUnscoped
	occurrence.Evidence.CitedEventIDs = []string{event.EventID}
	occurrence.AnalysisGeneration = appendResult.ReadGeneration
	replaceAttentionFamilyProjection(t, store, sessionID, occurrence)
	return occurrence
}

func addAttentionFamilyOccurrence(
	t *testing.T,
	store *Store,
	sessionID string,
	harness string,
	findingID string,
	rawRuleVersion string,
	observedAt time.Time,
	sourceSeverity string,
) model.IssueOccurrence {
	t.Helper()
	ctx := context.Background()
	event := storageTestEvent(
		fmt.Sprintf(
			"00000000-0000-7000-8000-%012x",
			uint64(observedAt.UnixNano())&0xffffffffffff,
		),
		sessionID,
		1,
		observedAt,
	)
	event.Source.Agent = harness
	event.Source.RunID = "run-" + sessionID
	event.Observation.Type = "config.agent"
	appendResult, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordFindingResolved(ctx, Finding{
		FindingID:     findingID,
		SourceRunID:   event.Source.RunID,
		SessionKey:    sessionID,
		DetectedAt:    observedAt,
		RuleID:        sourcecatalog.GuardrailsSourceSignalCode,
		RuleVersion:   rawRuleVersion,
		Severity:      sourceSeverity,
		SourceAgent:   harness,
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	}); err != nil {
		t.Fatal(err)
	}
	fingerprintID, issueID, err := store.DeriveIssueIdentity(
		"1",
		"numbat_finding",
		sessionID,
		"numbat",
		sourcecatalog.GuardrailsSourceSignalCode,
		rawRuleVersion,
		event.Observation.Type,
	)
	if err != nil {
		t.Fatal(err)
	}
	code := sourcecatalog.GuardrailsSourceSignalCode
	occurrence := testIssueOccurrence(
		sessionID,
		harness,
		event.EventID,
		fingerprintID,
		issueID,
		sourceSeverity,
		"high",
		observedAt,
		model.ScopeUnscoped,
	)
	occurrence.OccurrenceID = fmt.Sprintf("occ-%s", sessionID)
	occurrence.Origin = "numbat"
	occurrence.OriginRecordID = findingID
	occurrence.FingerprintVersion = "1"
	occurrence.Provenance = model.DetectorProvenance{
		DetectorID:         "numbat_finding",
		DetectorVersion:    sourcecatalog.OpaqueRuleVersion(rawRuleVersion),
		FingerprintVersion: "1",
		ProjectionVersion:  "1",
	}
	occurrence.Category = "numbat_finding"
	occurrence.TitleCode = "issue.numbat_finding"
	occurrence.SourceSignalCode = &code
	occurrence.Severity = sourceSeverity
	occurrence.Confidence = "high"
	occurrence.ScopeQuality = model.ScopeUnscoped
	occurrence.Evidence.CitedEventIDs = []string{event.EventID}
	occurrence.AnalysisGeneration = appendResult.ReadGeneration
	replaceAttentionFamilyProjection(t, store, sessionID, occurrence)
	return occurrence
}

func addBelayAttentionFamilyOccurrence(
	t *testing.T,
	store *Store,
	sessionID string,
	harness string,
	observedAt time.Time,
) model.IssueOccurrence {
	t.Helper()
	ctx := context.Background()
	event := storageTestEvent(
		fmt.Sprintf(
			"00000000-0000-7000-8000-%012x",
			uint64(observedAt.UnixNano())&0xffffffffffff,
		),
		sessionID,
		1,
		observedAt,
	)
	event.Source.Agent = harness
	event.Source.RunID = "run-" + sessionID
	appendResult, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	fingerprintID, issueID, err := store.DeriveIssueIdentity(
		"1",
		"explicit_command_failure",
		sessionID,
		"belay",
		"explicit_command_failure",
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := testIssueOccurrence(
		sessionID,
		harness,
		event.EventID,
		fingerprintID,
		issueID,
		"high",
		"high",
		observedAt,
		model.ScopeResolved,
	)
	occurrence.OccurrenceID = "occ-" + sessionID
	occurrence.Origin = "belay"
	occurrence.Severity = "high"
	occurrence.ScopeQuality = model.ScopeResolved
	occurrence.AnalysisGeneration = appendResult.ReadGeneration
	replaceAttentionFamilyProjection(t, store, sessionID, occurrence)
	return occurrence
}

func replaceAttentionFamilyProjection(
	t *testing.T,
	store *Store,
	sessionID string,
	occurrence model.IssueOccurrence,
) {
	t.Helper()
	var generation int64
	if err := store.db.QueryRow(`
		SELECT target_generation
		FROM dirty_sessions
		WHERE session_key = ?`,
		sessionID,
	).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	occurrence.AnalysisGeneration = generation
	if _, err := store.ReplaceIssueProjection(
		context.Background(),
		ProjectionReplacement{
			SessionKey:        sessionID,
			Origin:            occurrence.Origin,
			ClaimedGeneration: generation,
			Status:            model.AnalysisCurrent,
			ScopeQuality:      occurrence.ScopeQuality,
			Occurrences:       []model.IssueOccurrence{occurrence},
		},
	); err != nil {
		t.Fatal(err)
	}
}
