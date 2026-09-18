package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
)

func (s *Store) QueryAttentionFamilies(
	ctx context.Context,
	query model.AttentionFamilyQuery,
) (model.AttentionFamilyPage, error) {
	query.Limit = boundedReadLimit(query.Limit, 20, 100)
	if err := normalizeAttentionFamilyFilter(&query.Filter); err != nil {
		return model.AttentionFamilyPage{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		if ctx.Err() != nil {
			return model.AttentionFamilyPage{}, ctx.Err()
		}
		return model.AttentionFamilyPage{}, errors.New("begin attention family snapshot read")
	}
	defer tx.Rollback()
	state, err := s.issueMaterializedSnapshot(
		ctx,
		tx,
		query.CursorEpoch,
		query.Snapshot,
		query.RetentionGeneration,
		query.IssuedAt,
	)
	if err != nil {
		return model.AttentionFamilyPage{}, err
	}
	summaries, err := s.queryAttentionFamilyPageTx(
		ctx, tx, state.snapshot, query.Filter, query.Cursor, query.Limit+1,
	)
	if err != nil {
		return model.AttentionFamilyPage{}, err
	}
	hasMore := len(summaries) > query.Limit
	if hasMore {
		summaries = summaries[:query.Limit]
	}
	coverage, err := materializedIssueAnalysisCoverage(ctx, tx, state.snapshot)
	if err != nil {
		return model.AttentionFamilyPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.AttentionFamilyPage{}, errors.New("complete attention family snapshot read")
	}
	return model.AttentionFamilyPage{
		Data:                summaries,
		Analysis:            coverage,
		CursorEpoch:         state.epoch,
		Snapshot:            state.snapshot,
		RetentionGeneration: state.retentionGeneration,
		IssuedAt:            state.issuedAt,
		HasMore:             hasMore,
	}, nil
}

func (s *Store) QueryAttentionFamilyMembers(
	ctx context.Context,
	query model.AttentionFamilyMemberQuery,
) (model.AttentionFamilyMemberPage, error) {
	query.Limit = boundedReadLimit(query.Limit, 20, 100)
	if query.FamilyID == "" || query.GroupKey == "" {
		return model.AttentionFamilyMemberPage{}, errors.New("attention family identity is required")
	}
	if err := normalizeAttentionFamilyFilter(&query.Filter); err != nil {
		return model.AttentionFamilyMemberPage{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		if ctx.Err() != nil {
			return model.AttentionFamilyMemberPage{}, ctx.Err()
		}
		return model.AttentionFamilyMemberPage{}, errors.New("begin attention family member read")
	}
	defer tx.Rollback()
	state, err := s.issueMaterializedSnapshot(
		ctx,
		tx,
		query.CursorEpoch,
		query.Snapshot,
		query.RetentionGeneration,
		query.IssuedAt,
	)
	if err != nil {
		return model.AttentionFamilyMemberPage{}, err
	}
	mapping, ok := sourcecatalog.MappingByKey(
		sourcecatalog.GuardrailsConfigurationMappingKey, "1", "1",
	)
	if !ok {
		return model.AttentionFamilyMemberPage{}, errors.New("attention family catalog is unavailable")
	}
	expectedFamilyID, err := s.DeriveAttentionFamilyID(
		query.GroupKey, sourcecatalog.CatalogVersion, mapping.GroupingVersion,
	)
	if err != nil {
		return model.AttentionFamilyMemberPage{}, err
	}
	coverage, err := materializedIssueAnalysisCoverage(ctx, tx, state.snapshot)
	if err != nil {
		return model.AttentionFamilyMemberPage{}, err
	}
	if expectedFamilyID != query.FamilyID {
		if err := tx.Commit(); err != nil {
			return model.AttentionFamilyMemberPage{}, errors.New("complete absent attention family read")
		}
		return model.AttentionFamilyMemberPage{
			Analysis:            coverage,
			CursorEpoch:         state.epoch,
			Snapshot:            state.snapshot,
			RetentionGeneration: state.retentionGeneration,
			IssuedAt:            state.issuedAt,
		}, nil
	}
	family, found, err := s.queryAttentionFamilySummaryTx(
		ctx, tx, state.snapshot, query.Filter, query.GroupKey,
	)
	if err != nil {
		return model.AttentionFamilyMemberPage{}, err
	}
	if !found || family.Kind != model.AttentionFamilyKindMappedUpstream {
		if err := tx.Commit(); err != nil {
			return model.AttentionFamilyMemberPage{}, errors.New("complete absent attention family read")
		}
		return model.AttentionFamilyMemberPage{
			Analysis:            coverage,
			CursorEpoch:         state.epoch,
			Snapshot:            state.snapshot,
			RetentionGeneration: state.retentionGeneration,
			IssuedAt:            state.issuedAt,
		}, nil
	}
	members, err := s.queryAttentionFamilyMemberPageTx(
		ctx, tx, state.snapshot, query.Filter, query.GroupKey,
		query.Cursor, query.Limit+1,
	)
	if err != nil {
		return model.AttentionFamilyMemberPage{}, err
	}
	hasMore := len(members) > query.Limit
	if hasMore {
		members = members[:query.Limit]
	}
	if err := tx.Commit(); err != nil {
		return model.AttentionFamilyMemberPage{}, errors.New("complete attention family member read")
	}
	return model.AttentionFamilyMemberPage{
		Family:              family,
		Data:                members,
		Analysis:            coverage,
		CursorEpoch:         state.epoch,
		Snapshot:            state.snapshot,
		RetentionGeneration: state.retentionGeneration,
		IssuedAt:            state.issuedAt,
		HasMore:             hasMore,
		Found:               true,
	}, nil
}

func (s *Store) queryAttentionFamilyPageTx(
	ctx context.Context,
	tx *sql.Tx,
	snapshot int64,
	filter model.AttentionFamilyFilter,
	cursor *model.AttentionFamilyPosition,
	limit int,
) ([]model.AttentionFamilySummary, error) {
	cte, args, err := attentionFamilyCTE(snapshot, filter, "")
	if err != nil {
		return nil, err
	}
	where := []string{"1 = 1"}
	if cursor != nil {
		where = append(where, `(
			fr.severity_rank < ? OR
			(fr.severity_rank = ? AND fr.supporting_issue_count < ?) OR
			(fr.severity_rank = ? AND fr.supporting_issue_count = ? AND fr.last_observed_at < ?) OR
			(fr.severity_rank = ? AND fr.supporting_issue_count = ? AND fr.last_observed_at = ? AND fr.group_key > ?)
		)`)
		positionTime := formatProjectionTime(cursor.LastObserved)
		args = append(args,
			cursor.SeverityRank,
			cursor.SeverityRank, cursor.SupportingIssueCount,
			cursor.SeverityRank, cursor.SupportingIssueCount, positionTime,
			cursor.SeverityRank, cursor.SupportingIssueCount, positionTime, cursor.GroupKey,
		)
	}
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, cte+`
		SELECT `+attentionFamilySummaryColumns("fr", "representative")+`
		FROM family_rollup fr
		JOIN ranked_members representative
			ON representative.group_key = fr.group_key
			AND representative.family_member_rank = 1
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY fr.severity_rank DESC, fr.supporting_issue_count DESC,
			fr.last_observed_at DESC, fr.group_key ASC
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("query attention family page: %w", err)
	}
	defer rows.Close()
	result := make([]model.AttentionFamilySummary, 0, limit)
	for rows.Next() {
		family, err := s.scanAttentionFamilySummary(rows, filter.AttentionKind)
		if err != nil {
			return nil, err
		}
		result = append(result, family)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read attention family page")
	}
	return result, nil
}

func (s *Store) queryAttentionFamilySummaryTx(
	ctx context.Context,
	tx *sql.Tx,
	snapshot int64,
	filter model.AttentionFamilyFilter,
	groupKey string,
) (model.AttentionFamilySummary, bool, error) {
	cte, args, err := attentionFamilyCTE(snapshot, filter, groupKey)
	if err != nil {
		return model.AttentionFamilySummary{}, false, err
	}
	args = append(args, groupKey)
	row := tx.QueryRowContext(ctx, cte+`
		SELECT `+attentionFamilySummaryColumns("fr", "representative")+`
		FROM family_rollup fr
		JOIN ranked_members representative
			ON representative.group_key = fr.group_key
			AND representative.family_member_rank = 1
		WHERE fr.group_key = ?
		LIMIT 1`, args...)
	family, err := s.scanAttentionFamilySummary(row, filter.AttentionKind)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AttentionFamilySummary{}, false, nil
	}
	if err != nil {
		return model.AttentionFamilySummary{}, false, err
	}
	return family, true, nil
}

func (s *Store) queryAttentionFamilyMemberPageTx(
	ctx context.Context,
	tx *sql.Tx,
	snapshot int64,
	filter model.AttentionFamilyFilter,
	groupKey string,
	cursor *model.AttentionFamilyMemberPosition,
	limit int,
) ([]model.AttentionFamilyMemberRecord, error) {
	cte, args, err := attentionFamilyCTE(snapshot, filter, groupKey)
	if err != nil {
		return nil, err
	}
	where := []string{"im.group_key = ?"}
	args = append(args, groupKey)
	if cursor != nil {
		where = append(where, `(
			im.analysis_status_rank < ? OR
			(im.analysis_status_rank = ? AND im.last_observed_at < ?) OR
			(im.analysis_status_rank = ? AND im.last_observed_at = ? AND im.issue_id > ?)
		)`)
		positionTime := formatProjectionTime(cursor.LastObserved)
		args = append(args,
			cursor.AnalysisStatusRank,
			cursor.AnalysisStatusRank, positionTime,
			cursor.AnalysisStatusRank, positionTime, cursor.IssueID,
		)
	}
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, cte+`,
		member_page AS (
			SELECT im.*
			FROM issue_members im
			WHERE `+strings.Join(where, " AND ")+`
			ORDER BY im.analysis_status_rank DESC,
				im.last_observed_at DESC, im.issue_id ASC
			LIMIT ?
		),
		ranked_member_sessions AS (
			SELECT
				mp.summary_revision_id,
				mo.matched_session_key,
				ROW_NUMBER() OVER (
					PARTITION BY mp.summary_revision_id
					ORDER BY mo.matched_last_observed_at DESC,
						mo.matched_session_key ASC,
						mo.matched_occurrence_id ASC,
						mo.matched_revision_id ASC
				) AS session_rank
			FROM member_page mp
			JOIN matched_occurrences mo
				ON mo.summary_revision_id = mp.summary_revision_id
		),
		selected_member_sessions AS (
			SELECT summary_revision_id, matched_session_key
			FROM ranked_member_sessions
			WHERE session_rank = 1
		),
		member_session_bounds AS (
			SELECT
				sms.summary_revision_id,
				MIN(e.occurred_at) AS session_started_at,
				MAX(e.occurred_at) AS session_last_active_at
			FROM selected_member_sessions sms
			LEFT JOIN events e ON e.session_key = sms.matched_session_key
			GROUP BY sms.summary_revision_id
		),
		member_evidence_bounds AS (
			SELECT
				sms.summary_revision_id,
				COUNT(DISTINCT ioe.event_id) AS cited_event_count,
				MIN(cited.occurred_at) AS evidence_first_at,
				MAX(cited.occurred_at) AS evidence_last_at
			FROM selected_member_sessions sms
			LEFT JOIN matched_occurrences mo
				ON mo.summary_revision_id = sms.summary_revision_id
				AND mo.matched_session_key = sms.matched_session_key
			LEFT JOIN issue_occurrence_events ioe
				ON ioe.revision_id = mo.matched_revision_id
			LEFT JOIN events cited
				ON cited.event_id = ioe.event_id
				AND cited.session_key = sms.matched_session_key
			GROUP BY sms.summary_revision_id
		),
		issue_member_context AS (
			SELECT
				mp.*,
				sms.matched_session_key AS member_session_key,
				msb.session_started_at,
				msb.session_last_active_at,
				COALESCE(meb.cited_event_count, 0) AS cited_event_count,
				meb.evidence_first_at,
				meb.evidence_last_at
			FROM member_page mp
			JOIN selected_member_sessions sms
				ON sms.summary_revision_id = mp.summary_revision_id
			LEFT JOIN member_session_bounds msb
				ON msb.summary_revision_id = mp.summary_revision_id
			LEFT JOIN member_evidence_bounds meb
				ON meb.summary_revision_id = mp.summary_revision_id
		)
		SELECT `+attentionFamilyMemberRecordColumns("im")+`
		FROM issue_member_context im
		ORDER BY im.analysis_status_rank DESC, im.last_observed_at DESC, im.issue_id ASC
		`, args...)
	if err != nil {
		return nil, fmt.Errorf("query attention family members: %w", err)
	}
	defer rows.Close()
	result := make([]model.AttentionFamilyMemberRecord, 0, limit)
	for rows.Next() {
		member, err := scanAttentionFamilyMemberRecord(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, member)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read attention family members")
	}
	return result, nil
}

func attentionFamilyCTE(
	snapshot int64,
	filter model.AttentionFamilyFilter,
	requiredGroupKey string,
) (string, []any, error) {
	mapping, ok := sourcecatalog.MappingByKey(
		sourcecatalog.GuardrailsConfigurationMappingKey, "1", "1",
	)
	if !ok || len(mapping.RawRuleVersions) != 1 ||
		len(mapping.FingerprintVersions) != 1 {
		return "", nil, errors.New("attention family catalog is unavailable")
	}
	summaryClauses := []string{
		"sr.visible_from_generation <= ?",
		"(sr.visible_until_generation IS NULL OR sr.visible_until_generation > ?)",
	}
	args := []any{
		mapping.Key,
		mapping.Version,
		mapping.GroupingVersion,
		mapping.Origin,
		mapping.TitleCode,
		mapping.Category,
		mapping.SourceSignalCode,
		mapping.RawRuleVersions[0],
		mapping.FingerprintVersions[0],
		mapping.AttentionSeverity,
		familySeverityRank(mapping.AttentionSeverity),
		snapshot,
		snapshot,
	}
	switch filter.AttentionKind {
	case model.AttentionKindIssue:
		summaryClauses = append(summaryClauses, "sr.category <> 'evidence_gap'")
	case model.AttentionKindEvidenceGap:
		summaryClauses = append(summaryClauses, "sr.category = 'evidence_gap'")
	default:
		return "", nil, errors.New("unsupported attention family kind")
	}
	if filter.Experimental == model.ExperimentalStable {
		summaryClauses = append(summaryClauses, "sr.experimental = 0")
	}
	if filter.Origin != "" {
		summaryClauses = append(summaryClauses, "sr.origin = ?")
		args = append(args, filter.Origin)
	}
	if filter.AnalysisStatus != "" {
		summaryClauses = append(summaryClauses, "sr.analysis_status = ?")
		args = append(args, filter.AnalysisStatus)
	}
	args = append(args, snapshot, snapshot)

	occurrenceClauses := []string{
		"io.visible_from_generation <= ?",
		"(io.visible_until_generation IS NULL OR io.visible_until_generation > ?)",
	}
	occurrenceArgs := []any{snapshot, snapshot}
	if filter.Harness != "" {
		occurrenceClauses = append(occurrenceClauses, "LOWER(io.harness) = LOWER(?)")
		occurrenceArgs = append(occurrenceArgs, filter.Harness)
	}
	if filter.ObservedAfter != nil {
		occurrenceClauses = append(occurrenceClauses, "io.last_observed_at >= ?")
		occurrenceArgs = append(occurrenceArgs, formatProjectionTime(*filter.ObservedAfter))
	}

	selectedClauses := make([]string, 0, 2)
	if filter.Severity != "" {
		selectedClauses = append(selectedClauses, "classified.effective_severity = ?")
		args = append(args, filter.Severity)
	}
	if requiredGroupKey != "" {
		selectedClauses = append(selectedClauses, "classified.group_key = ?")
		args = append(args, requiredGroupKey)
	}
	args = append(args, occurrenceArgs...)
	selectedWhere := ""
	if len(selectedClauses) > 0 {
		selectedWhere = "WHERE " + strings.Join(selectedClauses, " AND ")
	}

	return `
		WITH
		mapping AS (
			SELECT
				? AS mapping_key,
				? AS mapping_version,
				? AS grouping_version,
				? AS origin,
				? AS title_code,
				? AS category,
				? AS source_signal_code,
				? AS raw_rule_version,
				? AS fingerprint_version,
				? AS attention_severity,
				? AS attention_severity_rank
		),
		classified AS (
			SELECT
				sr.*,
				CASE
					WHEN sr.origin = 'belay' THEN 'exact_issue'
					ELSE 'mapped_upstream'
				END AS family_kind,
				CASE
					WHEN sr.origin = 'belay' THEN 'exact:' || sr.issue_id
					ELSE 'mapped:' || m.mapping_key || ':' || m.mapping_version ||
						':' || m.grouping_version || ':' || sr.fingerprint_version
				END AS group_key,
				CASE WHEN sr.origin = 'belay' THEN '' ELSE m.mapping_key END AS mapping_key,
				CASE WHEN sr.origin = 'belay' THEN '' ELSE m.mapping_version END AS mapping_version,
				CASE WHEN sr.origin = 'belay' THEN '1' ELSE m.grouping_version END AS grouping_version,
				CASE WHEN sr.origin = 'belay' THEN sr.severity ELSE m.attention_severity END
					AS effective_severity,
				CASE WHEN sr.origin = 'belay' THEN sr.severity_rank
					ELSE m.attention_severity_rank END AS effective_severity_rank,
				CASE sr.confidence WHEN 'low' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END
					AS confidence_rank,
				CASE sr.analysis_status
					WHEN 'failed' THEN 4
					WHEN 'pending' THEN 3
					WHEN 'truncated' THEN 2
					ELSE 1
				END AS analysis_status_rank
			FROM issue_summary_revisions sr
			CROSS JOIN mapping m
			WHERE ` + strings.Join(summaryClauses, " AND ") + `
				AND (
					sr.origin = 'belay' OR (
						sr.origin = m.origin
						AND sr.title_code = m.title_code
						AND sr.category = m.category
						AND sr.source_signal_code = m.source_signal_code
						AND sr.fingerprint_version = m.fingerprint_version
						AND NOT EXISTS (
							SELECT 1
							FROM issue_occurrences io_check
							LEFT JOIN findings f_check
								ON f_check.finding_id = io_check.origin_record_id
							WHERE io_check.issue_id = sr.issue_id
								AND io_check.visible_from_generation <= ?
								AND (
									io_check.visible_until_generation IS NULL OR
									io_check.visible_until_generation > ?
								)
								AND (
									io_check.origin <> m.origin OR
									COALESCE(io_check.origin_record_id, '') = '' OR
									COALESCE(f_check.finding_id, '') <> io_check.origin_record_id OR
									COALESCE(f_check.session_key, '') <> io_check.session_key OR
									COALESCE(f_check.rule_id, '') <> m.source_signal_code OR
									COALESCE(f_check.rule_version, '') <> m.raw_rule_version
								)
						)
					)
				)
		),
		selected_summaries AS (
			SELECT classified.*
			FROM classified
			` + selectedWhere + `
		),
		matched_occurrences AS (
			SELECT
				ss.*,
				io.revision_id AS matched_revision_id,
				io.occurrence_id AS matched_occurrence_id,
				io.session_key AS matched_session_key,
				LOWER(io.harness) AS matched_harness,
				io.first_observed_at AS matched_first_observed_at,
				io.last_observed_at AS matched_last_observed_at
			FROM selected_summaries ss
			JOIN issue_occurrences io ON io.issue_id = ss.issue_id
			WHERE ` + strings.Join(occurrenceClauses, " AND ") + `
		),
		issue_members AS (
			SELECT
				mo.summary_revision_id,
				mo.issue_id,
				mo.fingerprint_id,
				mo.fingerprint_version,
				mo.origin,
				mo.detector_id,
				mo.detector_version,
				mo.category,
				mo.title_code,
				mo.source_signal_code,
				mo.severity,
				mo.confidence,
				mo.confidence_rank,
				mo.scope_quality,
				mo.analysis_status,
				mo.analysis_status_rank,
				mo.evidence_complete,
				mo.retained_history_only,
				mo.experimental,
				mo.family_kind,
				mo.group_key,
				mo.mapping_key,
				mo.mapping_version,
				mo.grouping_version,
				mo.effective_severity,
				mo.effective_severity_rank,
				MIN(mo.matched_first_observed_at) AS first_observed_at,
				MAX(mo.matched_last_observed_at) AS last_observed_at,
				COUNT(*) AS occurrence_count,
				COUNT(DISTINCT mo.matched_session_key) AS session_count,
				GROUP_CONCAT(DISTINCT mo.matched_harness) AS harnesses
			FROM matched_occurrences mo
			GROUP BY mo.summary_revision_id
		),
		family_occurrences AS (
			SELECT
				mo.group_key,
				COUNT(DISTINCT mo.matched_session_key) AS session_count,
				GROUP_CONCAT(DISTINCT mo.matched_harness) AS harnesses
			FROM matched_occurrences mo
			GROUP BY mo.group_key
		),
		family_rollup AS (
			SELECT
				im.group_key,
				MIN(im.family_kind) AS family_kind,
				MIN(im.mapping_key) AS mapping_key,
				MIN(im.mapping_version) AS mapping_version,
				MIN(im.grouping_version) AS grouping_version,
				MIN(im.effective_severity) AS severity,
				MIN(im.effective_severity_rank) AS severity_rank,
				MIN(im.confidence_rank) AS confidence_rank,
				MIN(im.first_observed_at) AS first_observed_at,
				MAX(im.last_observed_at) AS last_observed_at,
				COUNT(*) AS supporting_issue_count,
				SUM(im.occurrence_count) AS occurrence_count,
				fo.session_count,
				fo.harnesses,
				SUM(CASE WHEN im.scope_quality = 'resolved' THEN 1 ELSE 0 END)
					AS scope_resolved,
				SUM(CASE WHEN im.scope_quality = 'lexical' THEN 1 ELSE 0 END)
					AS scope_lexical,
				SUM(CASE WHEN im.scope_quality = 'unscoped' THEN 1 ELSE 0 END)
					AS scope_unscoped,
				SUM(CASE WHEN im.scope_quality = 'conflict' THEN 1 ELSE 0 END)
					AS scope_conflict,
				MAX(im.analysis_status_rank) AS analysis_status_rank,
				MIN(im.evidence_complete) AS evidence_complete,
				MIN(im.retained_history_only) AS retained_history_only,
				MAX(im.experimental) AS experimental
			FROM issue_members im
			JOIN family_occurrences fo ON fo.group_key = im.group_key
			GROUP BY im.group_key
		),
		ranked_members AS (
			SELECT
				im.*,
				ROW_NUMBER() OVER (
					PARTITION BY im.group_key
					ORDER BY im.analysis_status_rank DESC,
						im.last_observed_at DESC, im.issue_id ASC
				) AS family_member_rank
			FROM issue_members im
		)
	`, args, nil
}

func attentionFamilySummaryColumns(familyAlias, representativeAlias string) string {
	return strings.Join([]string{
		familyAlias + ".group_key",
		familyAlias + ".family_kind",
		familyAlias + ".mapping_key",
		familyAlias + ".mapping_version",
		familyAlias + ".grouping_version",
		familyAlias + ".severity",
		familyAlias + ".confidence_rank",
		familyAlias + ".first_observed_at",
		familyAlias + ".last_observed_at",
		familyAlias + ".supporting_issue_count",
		familyAlias + ".occurrence_count",
		familyAlias + ".session_count",
		"COALESCE(" + familyAlias + ".harnesses, '')",
		familyAlias + ".scope_resolved",
		familyAlias + ".scope_lexical",
		familyAlias + ".scope_unscoped",
		familyAlias + ".scope_conflict",
		familyAlias + ".analysis_status_rank",
		familyAlias + ".evidence_complete",
		familyAlias + ".retained_history_only",
		familyAlias + ".experimental",
		attentionFamilyMemberColumns(representativeAlias),
	}, ", ")
}

func attentionFamilyMemberColumns(alias string) string {
	return strings.Join([]string{
		alias + ".issue_id",
		alias + ".fingerprint_id",
		alias + ".fingerprint_version",
		alias + ".origin",
		alias + ".detector_id",
		alias + ".detector_version",
		alias + ".category",
		alias + ".title_code",
		alias + ".source_signal_code",
		alias + ".severity",
		alias + ".confidence",
		alias + ".scope_quality",
		alias + ".first_observed_at",
		alias + ".last_observed_at",
		alias + ".occurrence_count",
		alias + ".session_count",
		"COALESCE(" + alias + ".harnesses, '')",
		alias + ".analysis_status",
		alias + ".evidence_complete",
		alias + ".retained_history_only",
		alias + ".experimental",
	}, ", ")
}

func attentionFamilyMemberRecordColumns(alias string) string {
	return strings.Join([]string{
		attentionFamilyMemberColumns(alias),
		alias + ".member_session_key",
		alias + ".session_started_at",
		alias + ".session_last_active_at",
		alias + ".cited_event_count",
		alias + ".evidence_first_at",
		alias + ".evidence_last_at",
	}, ", ")
}

func (s *Store) scanAttentionFamilySummary(
	row rowScanner,
	attentionKind string,
) (model.AttentionFamilySummary, error) {
	var result model.AttentionFamilySummary
	representativeScan := attentionFamilyMemberScan{
		summary: &result.Representative,
	}
	var confidenceRankValue, analysisRank int
	var firstObserved, lastObserved, harnesses string
	var evidenceComplete, retainedHistoryOnly, experimental int
	targets := []any{
		&result.GroupKey,
		&result.Kind,
		&result.MappingKey,
		&result.MappingVersion,
		&result.GroupingVersion,
		&result.Severity,
		&confidenceRankValue,
		&firstObserved,
		&lastObserved,
		&result.SupportingIssueCount,
		&result.OccurrenceCount,
		&result.SessionCount,
		&harnesses,
		&result.Scope.Resolved,
		&result.Scope.Lexical,
		&result.Scope.Unscoped,
		&result.Scope.Conflict,
		&analysisRank,
		&evidenceComplete,
		&retainedHistoryOnly,
		&experimental,
	}
	targets = append(targets, representativeScan.targets()...)
	if err := row.Scan(targets...); err != nil {
		return model.AttentionFamilySummary{}, err
	}
	if err := representativeScan.finish(); err != nil {
		return model.AttentionFamilySummary{}, err
	}
	result.RepresentativeIssueID = result.Representative.IssueID
	result.AttentionKind = attentionKind
	result.Confidence = confidenceFromRank(confidenceRankValue)
	result.AnalysisStatus = analysisStatusFromRank(analysisRank)
	result.EvidenceComplete = evidenceComplete == 1
	result.RetainedHistoryOnly = retainedHistoryOnly == 1
	result.Experimental = experimental == 1
	result.Harnesses = splitSortedValues(harnesses)
	var err error
	result.FirstObservedAt, err = parseProjectionTime(firstObserved)
	if err != nil {
		return model.AttentionFamilySummary{}, errors.New("decode attention family first-observed timestamp")
	}
	result.LastObservedAt, err = parseProjectionTime(lastObserved)
	if err != nil {
		return model.AttentionFamilySummary{}, errors.New("decode attention family last-observed timestamp")
	}
	familyID, err := s.DeriveAttentionFamilyID(
		result.GroupKey, sourcecatalog.CatalogVersion, result.GroupingVersion,
	)
	if err != nil {
		return model.AttentionFamilySummary{}, err
	}
	result.FamilyID = familyID
	return result, nil
}

type attentionFamilyMemberScan struct {
	summary                                *model.IssueSummary
	sourceSignalCode                       sql.NullString
	firstObserved, lastObserved, harnesses string
	evidenceComplete, retainedHistoryOnly  int
	experimental                           int
}

func (scan *attentionFamilyMemberScan) targets() []any {
	summary := scan.summary
	return []any{
		&summary.IssueID,
		&summary.FingerprintID,
		&summary.FingerprintVersion,
		&summary.Origin,
		&summary.DetectorID,
		&summary.DetectorVersion,
		&summary.Category,
		&summary.TitleCode,
		&scan.sourceSignalCode,
		&summary.Severity,
		&summary.Confidence,
		&summary.ScopeQuality,
		&scan.firstObserved,
		&scan.lastObserved,
		&summary.OccurrenceCount,
		&summary.SessionCount,
		&scan.harnesses,
		&summary.AnalysisStatus,
		&scan.evidenceComplete,
		&scan.retainedHistoryOnly,
		&scan.experimental,
	}
}

func (scan *attentionFamilyMemberScan) finish() error {
	if scan == nil || scan.summary == nil {
		return errors.New("missing attention family member scan state")
	}
	summary := scan.summary
	if scan.sourceSignalCode.Valid {
		summary.SourceSignalCode = &scan.sourceSignalCode.String
	}
	var err error
	summary.FirstObservedAt, err = parseProjectionTime(scan.firstObserved)
	if err != nil {
		return errors.New("decode attention family member first-observed timestamp")
	}
	summary.LastObservedAt, err = parseProjectionTime(scan.lastObserved)
	if err != nil {
		return errors.New("decode attention family member last-observed timestamp")
	}
	summary.Harnesses = splitSortedValues(scan.harnesses)
	summary.EvidenceComplete = scan.evidenceComplete == 1
	summary.RetainedHistoryOnly = scan.retainedHistoryOnly == 1
	summary.Experimental = scan.experimental == 1
	return nil
}

func scanAttentionFamilyMemberRecord(
	row rowScanner,
) (model.AttentionFamilyMemberRecord, error) {
	var result model.AttentionFamilyMemberRecord
	scan := attentionFamilyMemberScan{summary: &result.IssueSummary}
	var sessionStarted, sessionLastActive, evidenceFirst, evidenceLast sql.NullString
	targets := append(scan.targets(),
		&result.SessionKey,
		&sessionStarted,
		&sessionLastActive,
		&result.CitedEventCount,
		&evidenceFirst,
		&evidenceLast,
	)
	if err := row.Scan(targets...); err != nil {
		return model.AttentionFamilyMemberRecord{}, err
	}
	if err := scan.finish(); err != nil {
		return model.AttentionFamilyMemberRecord{}, err
	}
	var err error
	result.SessionStartedAt, err = parseOptionalProjectionTime(sessionStarted)
	if err != nil {
		return model.AttentionFamilyMemberRecord{}, errors.New("decode attention family member session start")
	}
	result.SessionLastActiveAt, err = parseOptionalProjectionTime(sessionLastActive)
	if err != nil {
		return model.AttentionFamilyMemberRecord{}, errors.New("decode attention family member session activity")
	}
	result.EvidenceFirstAt, err = parseOptionalProjectionTime(evidenceFirst)
	if err != nil {
		return model.AttentionFamilyMemberRecord{}, errors.New("decode attention family member evidence start")
	}
	result.EvidenceLastAt, err = parseOptionalProjectionTime(evidenceLast)
	if err != nil {
		return model.AttentionFamilyMemberRecord{}, errors.New("decode attention family member evidence end")
	}
	result.SessionSelection = model.AttentionFamilyMemberSessionSelectionLatest
	return result, nil
}

func parseOptionalProjectionTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil, nil
	}
	parsed, err := parseProjectionTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func splitSortedValues(value string) []string {
	if value == "" {
		return nil
	}
	result := strings.Split(value, ",")
	sort.Strings(result)
	return result
}

func confidenceFromRank(rank int) string {
	switch rank {
	case 1:
		return "low"
	case 2:
		return "medium"
	default:
		return "high"
	}
}

func analysisStatusFromRank(rank int) model.AnalysisStatus {
	switch rank {
	case 4:
		return model.AnalysisFailed
	case 3:
		return model.AnalysisPending
	case 2:
		return model.AnalysisTruncated
	default:
		return model.AnalysisCurrent
	}
}

func normalizeAttentionFamilyFilter(filter *model.AttentionFamilyFilter) error {
	filter.Severity = strings.ToLower(strings.TrimSpace(filter.Severity))
	filter.Harness = strings.TrimSpace(filter.Harness)
	filter.Origin = strings.ToLower(strings.TrimSpace(filter.Origin))
	filter.AnalysisStatus = model.AnalysisStatus(
		strings.ToLower(strings.TrimSpace(string(filter.AnalysisStatus))),
	)
	filter.AttentionKind = strings.ToLower(strings.TrimSpace(filter.AttentionKind))
	filter.Experimental = strings.ToLower(strings.TrimSpace(filter.Experimental))
	if filter.ObservedAfter != nil {
		value := filter.ObservedAfter.UTC()
		filter.ObservedAfter = &value
	}
	if filter.AttentionKind == "" {
		filter.AttentionKind = model.AttentionKindIssue
	}
	if filter.Experimental == "" {
		filter.Experimental = model.ExperimentalStable
	}
	switch filter.Severity {
	case "", "info", "low", "medium", "high", "critical":
	default:
		return errors.New("unsupported attention family severity")
	}
	if len(filter.Harness) > 128 {
		return errors.New("attention family harness is too long")
	}
	switch filter.Origin {
	case "", "belay", "numbat":
	default:
		return errors.New("unsupported attention family origin")
	}
	switch filter.AnalysisStatus {
	case "", model.AnalysisCurrent, model.AnalysisPending,
		model.AnalysisFailed, model.AnalysisTruncated:
	default:
		return errors.New("unsupported attention family analysis status")
	}
	switch filter.AttentionKind {
	case model.AttentionKindIssue, model.AttentionKindEvidenceGap:
	default:
		return errors.New("unsupported attention family kind")
	}
	switch filter.Experimental {
	case model.ExperimentalStable, model.ExperimentalInclude:
	default:
		return errors.New("unsupported attention family experimental mode")
	}
	return nil
}

func attentionFamilyLess(
	left model.AttentionFamilySummary,
	right model.AttentionFamilySummary,
) bool {
	leftRank := familySeverityRank(left.Severity)
	rightRank := familySeverityRank(right.Severity)
	if leftRank != rightRank {
		return leftRank > rightRank
	}
	if left.SupportingIssueCount != right.SupportingIssueCount {
		return left.SupportingIssueCount > right.SupportingIssueCount
	}
	if !left.LastObservedAt.Equal(right.LastObservedAt) {
		return left.LastObservedAt.After(right.LastObservedAt)
	}
	return left.GroupKey < right.GroupKey
}

func attentionFamilyAfterPosition(
	value model.AttentionFamilySummary,
	position model.AttentionFamilyPosition,
) bool {
	rank := familySeverityRank(value.Severity)
	if rank != position.SeverityRank {
		return rank < position.SeverityRank
	}
	if value.SupportingIssueCount != position.SupportingIssueCount {
		return value.SupportingIssueCount < position.SupportingIssueCount
	}
	if !value.LastObservedAt.Equal(position.LastObserved) {
		return value.LastObservedAt.Before(position.LastObserved)
	}
	return value.GroupKey > position.GroupKey
}

func attentionFamilyMemberLess(
	left, right model.AttentionFamilyMemberRecord,
) bool {
	leftRank := analysisStatusRank(left.AnalysisStatus)
	rightRank := analysisStatusRank(right.AnalysisStatus)
	if leftRank != rightRank {
		return leftRank > rightRank
	}
	if !left.LastObservedAt.Equal(right.LastObservedAt) {
		return left.LastObservedAt.After(right.LastObservedAt)
	}
	return left.IssueID < right.IssueID
}

func attentionFamilyMemberAfterPosition(
	value model.AttentionFamilyMemberRecord,
	position model.AttentionFamilyMemberPosition,
) bool {
	rank := analysisStatusRank(value.AnalysisStatus)
	if rank != position.AnalysisStatusRank {
		return rank < position.AnalysisStatusRank
	}
	if !value.LastObservedAt.Equal(position.LastObserved) {
		return value.LastObservedAt.Before(position.LastObserved)
	}
	return value.IssueID > position.IssueID
}

func analysisStatusRank(status model.AnalysisStatus) int {
	switch status {
	case model.AnalysisFailed:
		return 4
	case model.AnalysisPending:
		return 3
	case model.AnalysisTruncated:
		return 2
	case model.AnalysisCurrent:
		return 1
	default:
		return 0
	}
}

func familySeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

func confidenceRank(confidence string) int {
	switch confidence {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	default:
		return 0
	}
}

func incrementScopeCount(
	counts *model.AttentionFamilyScopeCounts,
	quality model.ScopeQuality,
) {
	switch quality {
	case model.ScopeResolved:
		counts.Resolved++
	case model.ScopeLexical:
		counts.Lexical++
	case model.ScopeUnscoped:
		counts.Unscoped++
	case model.ScopeConflict:
		counts.Conflict++
	}
}
