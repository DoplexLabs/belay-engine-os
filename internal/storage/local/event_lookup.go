package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func (s *Store) LookupSessionEvents(
	ctx context.Context,
	query model.EventLookupQuery,
) (model.EventLookupResult, error) {
	events := make([]model.Event, 0, len(query.EventIDs))
	summary, err := s.VisitSessionEvents(
		ctx,
		query,
		func(event model.Event) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		return model.EventLookupResult{}, err
	}
	return model.EventLookupResult{
		Data:            events,
		RequestedCount:  summary.RequestedCount,
		FoundCount:      summary.FoundCount,
		MissingCount:    summary.MissingCount,
		MissingEventIDs: summary.MissingEventIDs,
		DataThrough:     summary.DataThrough,
	}, nil
}

func (s *Store) VisitSessionEvents(
	ctx context.Context,
	query model.EventLookupQuery,
	visit func(model.Event) error,
) (model.EventLookupSummary, error) {
	if query.SessionID == "" ||
		len(query.SessionID) > 256 ||
		len(query.EventIDs) == 0 ||
		len(query.EventIDs) > model.MaxEventLookupIDs ||
		visit == nil {
		return model.EventLookupSummary{}, errors.New("invalid event lookup request")
	}
	requested := make([]string, 0, len(query.EventIDs))
	seen := make(map[string]struct{}, len(query.EventIDs))
	for _, eventID := range query.EventIDs {
		if !model.IsCanonicalUUIDv7(eventID) {
			return model.EventLookupSummary{}, errors.New("invalid event lookup request")
		}
		if _, exists := seen[eventID]; exists {
			continue
		}
		seen[eventID] = struct{}{}
		requested = append(requested, eventID)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.EventLookupSummary{}, errors.New("begin event lookup")
	}
	defer tx.Rollback()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(requested)), ",")
	args := make([]any, 0, len(requested)+1)
	args = append(args, query.SessionID)
	for _, eventID := range requested {
		args = append(args, eventID)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT event_id, canonical_json, canonical_encoding
		FROM events
		WHERE session_key = ? AND event_id IN (`+placeholders+`)
		ORDER BY source_sequence ASC, occurred_at ASC, event_id ASC`,
		args...,
	)
	if err != nil {
		return model.EventLookupSummary{}, fmt.Errorf("lookup session events: %w", err)
	}
	found := make(map[string]struct{}, len(requested))
	foundCount := 0
	for rows.Next() {
		var eventID, encoding string
		var body []byte
		if err := rows.Scan(&eventID, &body, &encoding); err != nil {
			rows.Close()
			return model.EventLookupSummary{}, errors.New("read event lookup row")
		}
		event, err := s.decodeEvent(eventID, encoding, body)
		if err != nil {
			rows.Close()
			return model.EventLookupSummary{}, err
		}
		if err := visit(event); err != nil {
			rows.Close()
			return model.EventLookupSummary{}, err
		}
		found[eventID] = struct{}{}
		foundCount++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return model.EventLookupSummary{}, err
	}
	if err := rows.Close(); err != nil {
		return model.EventLookupSummary{}, errors.New("close event lookup rows")
	}
	dataThrough, err := dataThroughQuery(ctx, tx, "")
	if err != nil {
		return model.EventLookupSummary{}, errors.New("read event lookup watermark")
	}

	missing := make([]string, 0, len(requested)-foundCount)
	for _, eventID := range requested {
		if _, exists := found[eventID]; !exists {
			missing = append(missing, eventID)
		}
	}
	if err := tx.Commit(); err != nil {
		return model.EventLookupSummary{}, errors.New("complete event lookup")
	}
	return model.EventLookupSummary{
		RequestedCount:  len(requested),
		FoundCount:      foundCount,
		MissingCount:    len(missing),
		MissingEventIDs: missing,
		DataThrough:     dataThrough,
	}, nil
}
