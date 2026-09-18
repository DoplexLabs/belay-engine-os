package detection

import (
	"context"
	"fmt"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestSessionEventLimitBoundary(t *testing.T) {
	for _, count := range []int{MaxSessionEvents, MaxSessionEvents + 1} {
		t.Run(fmt.Sprintf("%d events", count), func(t *testing.T) {
			events := make([]model.Event, 0, count)
			for index := 0; index < count; index++ {
				events = append(events, testEvent(
					fmt.Sprintf("event-%05d", index),
					int64(index+1),
					"message",
				))
			}
			result := DefaultCatalog().Run(context.Background(), testInput(events...))
			if count == MaxSessionEvents {
				if result.Status != StatusCurrent {
					t.Fatalf("status at limit = %q, failures = %+v", result.Status, result.Failures)
				}
			} else if result.Status != StatusTruncated ||
				len(result.Matches) != 0 ||
				len(result.Failures) != 0 {
				t.Fatalf("result above limit = %+v", result)
			}
		})
	}
}

func TestCitationLimitBoundary(t *testing.T) {
	for _, count := range []int{MaxCitations, MaxCitations + 1} {
		t.Run(fmt.Sprintf("%d citations", count), func(t *testing.T) {
			input := testInput()
			for index := 0; index < count; index++ {
				id := fmt.Sprintf("failed-%03d", index)
				input.Events = append(input.Events, withOutcome(
					testEvent(id, int64(index+1), "command.result"),
					"failed",
					nil,
				))
				enrichCommand(&input, id, "sig-a", CommandClassOther)
			}
			result := DefaultCatalog().Run(context.Background(), input)
			requireCurrent(t, result)
			match, ok := findMatch(t, result, "explicit_command_failure")
			if !ok {
				t.Fatalf("missing command failure: %+v", result.Matches)
			}
			if len(match.CitedEventIDs) != MaxCitations {
				t.Fatalf("citation count = %d, want %d", len(match.CitedEventIDs), MaxCitations)
			}
			if got, want := match.EvidenceComplete, count == MaxCitations; got != want {
				t.Fatalf("evidence complete = %v, want %v", got, want)
			}
		})
	}
}

func TestMatchLimitBoundary(t *testing.T) {
	for _, count := range []int{MaxMatches, MaxMatches + 1} {
		t.Run(fmt.Sprintf("%d matches", count), func(t *testing.T) {
			input := testInput()
			for index := 0; index < count; index++ {
				id := fmt.Sprintf("failed-%03d", index)
				input.Events = append(input.Events, withOutcome(
					testEvent(id, int64(index+1), "command.result"),
					"failed",
					nil,
				))
				enrichCommand(
					&input,
					id,
					fmt.Sprintf("sig-%03d", index),
					CommandClassOther,
				)
			}
			result := DefaultCatalog().Run(context.Background(), input)
			if count == MaxMatches {
				if result.Status != StatusCurrent || len(result.Matches) != MaxMatches {
					t.Fatalf("result at match limit = status %q, matches %d, failures %+v",
						result.Status, len(result.Matches), result.Failures)
				}
			} else if result.Status != StatusTruncated ||
				len(result.Matches) != 0 ||
				len(result.Failures) != 0 {
				t.Fatalf("result above match limit = %+v", result)
			}
		})
	}
}
