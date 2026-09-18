package detection

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	failureCanceled      = "detector_canceled"
	failureError         = "detector_error"
	failureInvalidOutput = "detector_invalid_output"
	failurePanic         = "detector_panic"
	failureTimeout       = "detector_timeout"
)

type Catalog struct {
	slots   []*detectorSlot
	timeout time.Duration
}

type detectorSlot struct {
	detector Detector
	mu       sync.Mutex
	running  bool
}

func DefaultCatalog() Catalog {
	return catalogFromDetectors(BuiltinDetectors())
}

func NewCatalog(detectors ...Detector) (Catalog, error) {
	seen := make(map[string]struct{}, len(detectors))
	cloned := make([]Detector, 0, len(detectors))
	for _, detector := range detectors {
		if detector == nil || strings.TrimSpace(detector.ID()) == "" {
			return Catalog{}, errors.New("detector catalog contains an invalid detector")
		}
		if _, ok := seen[detector.ID()]; ok {
			return Catalog{}, errors.New("detector catalog contains a duplicate detector ID")
		}
		seen[detector.ID()] = struct{}{}
		cloned = append(cloned, detector)
	}
	return catalogFromDetectors(cloned), nil
}

func catalogFromDetectors(detectors []Detector) Catalog {
	slots := make([]*detectorSlot, 0, len(detectors))
	for _, detector := range detectors {
		slots = append(slots, &detectorSlot{detector: detector})
	}
	return Catalog{slots: slots, timeout: defaultDetectorTime}
}

func (catalog Catalog) WithDetectorTimeout(timeout time.Duration) Catalog {
	if timeout > 0 {
		catalog.timeout = timeout
	}
	return catalog
}

func (catalog Catalog) Entries() []CatalogEntry {
	entries := make([]CatalogEntry, 0, len(catalog.slots))
	for _, slot := range catalog.slots {
		detector := slot.detector
		if builtin, ok := detector.(builtinDetector); ok {
			entries = append(entries, builtin.entry)
			continue
		}
		entries = append(entries, CatalogEntry{
			DetectorID:         detector.ID(),
			DetectorVersion:    detector.Version(),
			FingerprintVersion: detector.FingerprintVersion(),
		})
	}
	return entries
}

func (catalog Catalog) Run(ctx context.Context, input SessionInput) CatalogResult {
	result := CatalogResult{
		CatalogVersion: CatalogVersion,
		Status:         StatusCurrent,
		Matches:        []Match{},
		Failures:       []DetectorFailure{},
		Applicability:  []DetectorApplicability{},
	}
	if ctx == nil {
		result.Status = StatusFailed
		result.Failures = append(result.Failures, DetectorFailure{Code: failureCanceled})
		return result
	}
	session, err := prepareSession(ctx, input)
	if errors.Is(err, errSessionLimit) {
		result.Status = StatusTruncated
		return result
	}
	if err != nil {
		result.Status = StatusFailed
		result.Failures = append(result.Failures, DetectorFailure{Code: failureCode(err)})
		return result
	}

	for _, slot := range catalog.slots {
		detector := slot.detector
		detectorResult, code := catalog.runDetector(ctx, slot, session)
		if code != "" {
			result.Failures = append(result.Failures, DetectorFailure{
				DetectorID: detector.ID(),
				Code:       code,
			})
			result.Applicability = append(result.Applicability, DetectorApplicability{
				DetectorID:         detector.ID(),
				DetectorVersion:    detector.Version(),
				FingerprintVersion: detector.FingerprintVersion(),
				AbsenceCapability:  AbsenceIncomplete,
				UnavailableReason:  code,
			})
			continue
		}
		result.Applicability = append(result.Applicability, DetectorApplicability{
			DetectorID:         detector.ID(),
			DetectorVersion:    detector.Version(),
			FingerprintVersion: detector.FingerprintVersion(),
			AbsenceCapability:  detectorResult.AbsenceCapability,
			UnavailableReason:  detectorResult.UnavailableReason,
		})
		matches := detectorResult.Matches
		for index := range matches {
			if err := validateMatch(detector, &matches[index]); err != nil {
				result.Failures = append(result.Failures, DetectorFailure{
					DetectorID: detector.ID(),
					Code:       failureInvalidOutput,
				})
				break
			}
			result.Matches = append(result.Matches, matches[index])
			if len(result.Matches) > MaxMatches {
				result.Status = StatusTruncated
				result.Matches = []Match{}
				result.Failures = []DetectorFailure{}
				for index := range result.Applicability {
					result.Applicability[index].AbsenceCapability = AbsenceIncomplete
					result.Applicability[index].UnavailableReason = "catalog_truncated"
				}
				return result
			}
		}
	}
	if len(result.Failures) > 0 {
		result.Status = StatusFailed
		result.Matches = []Match{}
		return result
	}
	if duplicateMatch(result.Matches) {
		result.Status = StatusFailed
		result.Matches = []Match{}
		result.Failures = []DetectorFailure{{Code: failureInvalidOutput}}
		return result
	}
	sortMatches(result.Matches)
	return result
}

type detectorResult struct {
	result DetectorResult
	err    error
	panic  bool
}

type preparedEvaluator interface {
	evaluatePrepared(context.Context, preparedSession) (DetectorResult, error)
}

func (catalog Catalog) runDetector(
	parent context.Context,
	slot *detectorSlot,
	session preparedSession,
) (DetectorResult, string) {
	detector := slot.detector
	timeout := catalog.timeout
	if timeout <= 0 {
		timeout = defaultDetectorTime
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if !slot.tryStart() {
		if errors.Is(parent.Err(), context.Canceled) {
			return DetectorResult{}, failureCanceled
		}
		return DetectorResult{}, failureTimeout
	}
	done := make(chan detectorResult, 1)
	go func() {
		response := detectorResult{}
		defer func() {
			if recover() != nil {
				response.result = DetectorResult{}
				response.err = nil
				response.panic = true
			}
			slot.finish()
			done <- response
		}()
		if prepared, ok := detector.(preparedEvaluator); ok {
			response.result, response.err = prepared.evaluatePrepared(ctx, session)
			return
		}
		response.result, response.err = detector.Evaluate(ctx, session.publicInput())
	}()

	select {
	case <-ctx.Done():
		if errors.Is(parent.Err(), context.Canceled) {
			return DetectorResult{}, failureCanceled
		}
		if errors.Is(parent.Err(), context.DeadlineExceeded) {
			return DetectorResult{}, failureTimeout
		}
		return DetectorResult{}, failureTimeout
	case response := <-done:
		switch {
		case response.panic:
			return DetectorResult{}, failurePanic
		case response.err != nil:
			return DetectorResult{}, failureCode(response.err)
		case response.result.AbsenceCapability != AbsenceSupported &&
			response.result.AbsenceCapability != AbsenceNotApplicable &&
			response.result.AbsenceCapability != AbsenceIncomplete:
			return DetectorResult{}, failureInvalidOutput
		default:
			if response.result.Matches == nil {
				response.result.Matches = []Match{}
			}
			return response.result, ""
		}
	}
}

func (slot *detectorSlot) tryStart() bool {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.running {
		return false
	}
	slot.running = true
	return true
}

func (slot *detectorSlot) finish() {
	slot.mu.Lock()
	slot.running = false
	slot.mu.Unlock()
}

func failureCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return failureCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return failureTimeout
	default:
		return failureError
	}
}

func validateMatch(detector Detector, match *Match) error {
	if match.DetectorID != detector.ID() ||
		match.DetectorVersion != detector.Version() ||
		match.FingerprintVersion != detector.FingerprintVersion() ||
		match.Category == "" ||
		match.TitleCode == "" ||
		match.Severity == "" ||
		match.Confidence == "" ||
		match.FirstObservedAt.IsZero() ||
		match.LastObservedAt.Before(match.FirstObservedAt) ||
		len(match.Fingerprint) == 0 {
		return errors.New("invalid detector match")
	}
	if len(match.CitedEventIDs) > MaxCitations {
		match.CitedEventIDs = append([]string(nil), match.CitedEventIDs[:MaxCitations]...)
		match.EvidenceComplete = false
	}
	for _, dimension := range match.Fingerprint {
		if dimension.Name == "" || dimension.Value == "" {
			return errors.New("invalid fingerprint dimension")
		}
	}
	sort.Slice(match.Fingerprint, func(i, j int) bool {
		if match.Fingerprint[i].Name != match.Fingerprint[j].Name {
			return match.Fingerprint[i].Name < match.Fingerprint[j].Name
		}
		return match.Fingerprint[i].Value < match.Fingerprint[j].Value
	})
	match.CitedEventIDs = sortedDistinct(match.CitedEventIDs)
	return nil
}

func duplicateMatch(matches []Match) bool {
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		key := match.DetectorID + "\x00" + dimensionKey(match.Fingerprint)
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
	}
	return false
}

func dimensionKey(dimensions []FingerprintDimension) string {
	var builder strings.Builder
	for _, dimension := range dimensions {
		_, _ = fmt.Fprintf(
			&builder,
			"%d:%s%d:%s",
			len(dimension.Name),
			dimension.Name,
			len(dimension.Value),
			dimension.Value,
		)
	}
	return builder.String()
}

func sortMatches(matches []Match) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].DetectorID != matches[j].DetectorID {
			return matches[i].DetectorID < matches[j].DetectorID
		}
		left := dimensionKey(matches[i].Fingerprint)
		right := dimensionKey(matches[j].Fingerprint)
		if left != right {
			return left < right
		}
		if !matches[i].FirstObservedAt.Equal(matches[j].FirstObservedAt) {
			return matches[i].FirstObservedAt.Before(matches[j].FirstObservedAt)
		}
		return matches[i].LastObservedAt.Before(matches[j].LastObservedAt)
	})
}

func sortedDistinct(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return compactStrings(result)
}
