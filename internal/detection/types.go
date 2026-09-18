// Package detection derives bounded, deterministic observations from canonical
// Belay events. It has no storage, acquisition, presentation, filesystem, or
// network dependency.
package detection

import (
	"context"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const (
	CatalogVersion      = "belay.detectors.v1"
	MaxSessionEvents    = 10_000
	MaxMatches          = 100
	MaxCitations        = 50
	defaultDetectorTime = time.Second
)

const (
	StatusCurrent   AnalysisStatus = "current"
	StatusFailed    AnalysisStatus = "failed"
	StatusTruncated AnalysisStatus = "truncated"
)

const (
	ConfidenceLow    = "low"
	ConfidenceMedium = "medium"
	ConfidenceHigh   = "high"
)

const (
	SeverityInfo   = "info"
	SeverityLow    = "low"
	SeverityMedium = "medium"
	SeverityHigh   = "high"
)

const (
	CommandClassTest        = "test"
	CommandClassBuild       = "build"
	CommandClassTypecheck   = "typecheck"
	CommandClassLint        = "lint"
	CommandClassFormatCheck = "format_check"
	CommandClassOther       = "other"
	CommandClassUnknown     = "unknown"
)

type AnalysisStatus string

type EventEnrichment struct {
	CommandSignatureID string `json:"command_signature_id,omitempty"`
	CommandClass       string `json:"command_class,omitempty"`
	PermissionClass    string `json:"permission_class,omitempty"`
	Version            string `json:"version,omitempty"`
}

type SessionInput struct {
	SessionID    string                     `json:"session_id"`
	ProjectScope string                     `json:"project_scope,omitempty"`
	ScopeQuality string                     `json:"scope_quality"`
	Events       []model.Event              `json:"events"`
	Enrichments  map[string]EventEnrichment `json:"enrichments,omitempty"`
}

type FingerprintDimension struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Match struct {
	DetectorID          string                 `json:"detector_id"`
	DetectorVersion     string                 `json:"detector_version"`
	FingerprintVersion  string                 `json:"fingerprint_version"`
	Category            string                 `json:"category"`
	TitleCode           string                 `json:"title_code"`
	SuggestedActionType string                 `json:"suggested_action_type"`
	Severity            string                 `json:"severity"`
	Confidence          string                 `json:"confidence"`
	Experimental        bool                   `json:"experimental"`
	FirstObservedAt     time.Time              `json:"first_observed_at"`
	LastObservedAt      time.Time              `json:"last_observed_at"`
	CitedEventIDs       []string               `json:"cited_event_ids"`
	EvidenceComplete    bool                   `json:"evidence_complete"`
	Fingerprint         []FingerprintDimension `json:"fingerprint_dimensions"`
}

type AbsenceCapability string

const (
	AbsenceSupported     AbsenceCapability = "supported"
	AbsenceNotApplicable AbsenceCapability = "not_applicable"
	AbsenceIncomplete    AbsenceCapability = "incomplete"
)

type DetectorResult struct {
	Matches           []Match           `json:"matches"`
	AbsenceCapability AbsenceCapability `json:"absence_capability"`
	UnavailableReason string            `json:"unavailable_reason,omitempty"`
}

type Detector interface {
	ID() string
	Version() string
	FingerprintVersion() string
	Evaluate(context.Context, SessionInput) (DetectorResult, error)
}

type DetectorFailure struct {
	DetectorID string `json:"detector_id"`
	Code       string `json:"code"`
}

type CatalogResult struct {
	CatalogVersion string                  `json:"catalog_version"`
	Status         AnalysisStatus          `json:"status"`
	Matches        []Match                 `json:"matches"`
	Failures       []DetectorFailure       `json:"failures"`
	Applicability  []DetectorApplicability `json:"applicability"`
}

type DetectorApplicability struct {
	DetectorID         string            `json:"detector_id"`
	DetectorVersion    string            `json:"detector_version"`
	FingerprintVersion string            `json:"fingerprint_version"`
	AbsenceCapability  AbsenceCapability `json:"absence_capability"`
	UnavailableReason  string            `json:"unavailable_reason,omitempty"`
}

type CatalogEntry struct {
	DetectorID          string `json:"detector_id"`
	DetectorVersion     string `json:"detector_version"`
	FingerprintVersion  string `json:"fingerprint_version"`
	Category            string `json:"category"`
	TitleCode           string `json:"title_code"`
	SuggestedActionType string `json:"suggested_action_type"`
	Experimental        bool   `json:"experimental"`
}
