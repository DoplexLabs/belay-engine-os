package local

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const (
	projectScopeNormalizationVersion  = "project-path.v1"
	numbatProjectScopeHashVersion     = "numbat-project-sha256.v1"
	commandNormalizationVersion       = "exact-command.v1"
	projectScopeKeyDomain             = "belay.local.project-scope.v1"
	numbatProjectScopeHashKeyDomain   = "belay.local.numbat-project-scope-hash.v1"
	commandSignatureKeyDomain         = "belay.local.command-signature.v1"
	issueFingerprintKeyDomain         = "belay.local.issue-fingerprint.v1"
	attentionFamilyKeyDomain          = "belay.local.attention-family.v1"
	issueCursorKeyDomain              = "belay.local.issue-cursor.v2"
	fixAnnotationIDKeyDomain          = "belay.local.fix-annotation.v1"
	fixAnnotationRequestKeyDomain     = "belay.local.fix-annotation-request.v1"
	fixActionTokenKeyDomain           = "belay.local.fix-action-token.v1"
	experienceApprovalTokenKeyDomain  = "belay.local.experience-approval-token.v1"
	experienceLifecycleTokenKeyDomain = "belay.local.experience-lifecycle-token.v1"
	fixRetractionIDKeyDomain          = "belay.local.fix-retraction.v1"
	fixRetractionRequestKeyDomain     = "belay.local.fix-retraction-request.v1"
	fixRecurrenceIDKeyDomain          = "belay.local.fix-recurrence-id.v1"
	fixRecurrenceJobIDKeyDomain       = "belay.local.fix-recurrence-job-id.v1"
	legacySessionScopeKeyDomain       = "belay.local.legacy-session-scope.v1"
)

var opaqueBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

type ProjectScope struct {
	ID                   string
	Quality              model.ScopeQuality
	NormalizationVersion string
}

func (s *Store) DeriveProjectScope(rawPath string) (ProjectScope, error) {
	normalized, quality, err := normalizeProjectPath(rawPath)
	if err != nil {
		return ProjectScope{}, err
	}
	key, err := s.derivedKey(projectScopeKeyDomain)
	if err != nil {
		return ProjectScope{}, err
	}
	defer zeroBytes(key)
	return ProjectScope{
		ID:                   opaqueID("psc_", key, []byte(normalized)),
		Quality:              quality,
		NormalizationVersion: projectScopeNormalizationVersion,
	}, nil
}

func (s *Store) DeriveNumbatProjectScopeHash(rawHash string) (ProjectScope, error) {
	if !validLowerHexSHA256(rawHash) {
		return ProjectScope{}, errors.New("Numbat project scope hash must be lowercase SHA-256")
	}
	key, err := s.derivedKey(numbatProjectScopeHashKeyDomain)
	if err != nil {
		return ProjectScope{}, err
	}
	defer zeroBytes(key)
	return ProjectScope{
		ID:                   opaqueID("psc_", key, []byte(rawHash)),
		Quality:              model.ScopeLexical,
		NormalizationVersion: numbatProjectScopeHashVersion,
	}, nil
}

func (s *Store) DeriveCommandSignature(rawCommand string) (string, error) {
	normalized, err := normalizeExactCommand(rawCommand)
	if err != nil {
		return "", err
	}
	key, err := s.derivedKey(commandSignatureKeyDomain)
	if err != nil {
		return "", err
	}
	defer zeroBytes(key)
	return opaqueID("cmd_", key, []byte(normalized)), nil
}

func (s *Store) DeriveIssueIdentity(
	fingerprintVersion string,
	detectorID string,
	projectScopeOrSession string,
	dimensions ...string,
) (fingerprintID string, issueID string, err error) {
	if fingerprintVersion == "" || detectorID == "" || projectScopeOrSession == "" {
		return "", "", errors.New("issue identity requires version, detector, and scope")
	}
	values := make([]string, 0, 3+len(dimensions))
	values = append(values, fingerprintVersion, detectorID, projectScopeOrSession)
	for _, dimension := range dimensions {
		if dimension == "" {
			return "", "", errors.New("issue identity dimensions cannot be empty")
		}
		values = append(values, dimension)
	}
	material := lengthPrefixed(values)
	key, err := s.derivedKey(issueFingerprintKeyDomain)
	if err != nil {
		return "", "", err
	}
	defer zeroBytes(key)
	digest := opaqueDigest(key, material)
	encoded := strings.ToLower(opaqueBase32.EncodeToString(digest))
	return "ifp_" + encoded, "iss_" + encoded, nil
}

func (s *Store) DeriveAttentionFamilyID(
	groupKey string,
	catalogVersion string,
	groupingVersion string,
) (string, error) {
	if groupKey == "" || catalogVersion == "" || groupingVersion == "" {
		return "", errors.New("attention family identity requires group and versions")
	}
	return s.deriveOpaqueID(
		attentionFamilyKeyDomain,
		"atf_",
		lengthPrefixed([]string{catalogVersion, groupingVersion, groupKey}),
	)
}

func (s *Store) deriveFixAnnotationID(idempotencyKey string) (string, error) {
	return s.deriveOpaqueID(
		fixAnnotationIDKeyDomain,
		"fxa_",
		lengthPrefixed([]string{idempotencyKey}),
	)
}

func (s *Store) currentIssueCursorEpoch() (string, error) {
	var epoch, readiness string
	var current, materialized int64
	if err := s.db.QueryRow(`
		SELECT ism.cursor_epoch, ism.readiness, ipm.current_generation,
			ism.materialized_generation
		FROM issue_summary_metadata ism
		JOIN issue_projection_metadata ipm ON ipm.singleton = ism.singleton
		WHERE ism.singleton = 1`,
	).Scan(&epoch, &readiness, &current, &materialized); err != nil {
		return "", errors.New("read issue cursor epoch")
	}
	if epoch == "" || readiness != "ready" || materialized != current {
		return "", model.ErrIssueSnapshotExpired
	}
	return epoch, nil
}

func (s *Store) deriveFixAnnotationRequestFingerprint(
	claims model.FixActionClaims,
	changeKind model.FixChangeKind,
	recordedVia string,
) (string, error) {
	return s.deriveOpaqueID(
		fixAnnotationRequestKeyDomain,
		"fxp_",
		lengthPrefixed([]string{
			model.FixSchemaVersion,
			claims.Version,
			claims.CursorEpoch,
			claims.IssueID,
			fmt.Sprint(claims.Snapshot),
			fmt.Sprint(claims.RetentionGeneration),
			formatProjectionTime(claims.IssuedAt),
			model.FixChangeCatalogVersion,
			string(changeKind),
			recordedVia,
		}),
	)
}

func (s *Store) deriveFixRetractionID(idempotencyKey string) (string, error) {
	return s.deriveOpaqueID(
		fixRetractionIDKeyDomain,
		"fxr_",
		lengthPrefixed([]string{idempotencyKey}),
	)
}

func (s *Store) deriveFixRetractionRequestFingerprint(
	issueID string,
	annotationID string,
	reason model.FixRetractionReason,
	recordedVia string,
) (string, error) {
	return s.deriveOpaqueID(
		fixRetractionRequestKeyDomain,
		"frp_",
		lengthPrefixed([]string{
			model.FixSchemaVersion,
			issueID,
			annotationID,
			string(reason),
			recordedVia,
		}),
	)
}

func (s *Store) deriveFixRecurrenceID(
	annotationID string,
	occurrenceID string,
) (string, error) {
	if !validFixAnnotationID(annotationID) || occurrenceID == "" {
		return "", errors.New("invalid recurrence identity")
	}
	return s.deriveOpaqueID(
		fixRecurrenceIDKeyDomain,
		"fxo_",
		lengthPrefixed([]string{annotationID, occurrenceID}),
	)
}

func (s *Store) deriveFixRecurrenceJobID(
	sessionID string,
	projectionGeneration int64,
) (string, error) {
	if sessionID == "" || projectionGeneration < 1 {
		return "", errors.New("invalid recurrence job identity")
	}
	return s.deriveOpaqueID(
		fixRecurrenceJobIDKeyDomain,
		"fxj_",
		lengthPrefixed([]string{sessionID, fmt.Sprint(projectionGeneration)}),
	)
}

func (s *Store) deriveLegacySessionScopeID(sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("legacy session scope requires a session")
	}
	return s.deriveOpaqueID(
		legacySessionScopeKeyDomain,
		"psc_",
		lengthPrefixed([]string{sessionID}),
	)
}

func (s *Store) deriveOpaqueID(domain, prefix string, material []byte) (string, error) {
	key, err := s.derivedKey(domain)
	if err != nil {
		return "", err
	}
	defer zeroBytes(key)
	return opaqueID(prefix, key, material), nil
}

func (s *Store) derivedKey(domain string) ([]byte, error) {
	if s.cipher == nil || len(s.cipher.keyBytes) != 32 {
		return nil, errors.New("local store key is unavailable")
	}
	key, err := hkdf.Key(
		sha256.New,
		s.cipher.keyBytes,
		[]byte(s.storeID),
		domain,
		32,
	)
	if err != nil {
		return nil, errors.New("derive local opaque identity key")
	}
	return key, nil
}

func normalizeProjectPath(rawPath string) (string, model.ScopeQuality, error) {
	if rawPath == "" || strings.IndexByte(rawPath, 0) >= 0 {
		return "", "", errors.New("project path is empty or invalid")
	}
	if !utf8.ValidString(rawPath) {
		return "", "", errors.New("project path is not valid UTF-8")
	}
	if !filepath.IsAbs(rawPath) {
		return "", "", errors.New("project path must be absolute")
	}
	normalized := filepath.Clean(rawPath)
	quality := model.ScopeLexical
	if resolved, err := filepath.EvalSymlinks(normalized); err == nil {
		normalized = filepath.Clean(resolved)
		quality = model.ScopeResolved
	}
	if runtime.GOOS == "darwin" {
		switch {
		case normalized == "/private/var":
			normalized = "/var"
		case strings.HasPrefix(normalized, "/private/var/"):
			normalized = "/var/" + strings.TrimPrefix(normalized, "/private/var/")
		case normalized == "/private/tmp":
			normalized = "/tmp"
		case strings.HasPrefix(normalized, "/private/tmp/"):
			normalized = "/tmp/" + strings.TrimPrefix(normalized, "/private/tmp/")
		}
	}
	normalized = filepath.ToSlash(normalized)
	if len(normalized) > 1 {
		normalized = strings.TrimSuffix(normalized, "/")
	}
	return normalized, quality, nil
}

func normalizeExactCommand(rawCommand string) (string, error) {
	if strings.IndexByte(rawCommand, 0) >= 0 || !utf8.ValidString(rawCommand) {
		return "", errors.New("command is not valid UTF-8")
	}
	normalized := strings.TrimSpace(rawCommand)
	if normalized == "" {
		return "", errors.New("command is empty")
	}
	return normalized, nil
}

func validLowerHexSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validProjectScopeHint(value string) bool {
	if len(value) != len("psc_")+52 || !strings.HasPrefix(value, "psc_") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "psc_") {
		if (character < 'a' || character > 'z') &&
			(character < '2' || character > '7') {
			return false
		}
	}
	return true
}

func opaqueID(prefix string, key, material []byte) string {
	return prefix + strings.ToLower(opaqueBase32.EncodeToString(opaqueDigest(key, material)))
}

func opaqueDigest(key, material []byte) []byte {
	hash := hmac.New(sha256.New, key)
	_, _ = hash.Write(material)
	return hash.Sum(nil)
}

func lengthPrefixed(values []string) []byte {
	size := 0
	for _, value := range values {
		size += 4 + len(value)
	}
	result := make([]byte, 0, size)
	var length [4]byte
	for _, value := range values {
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		result = append(result, length[:]...)
		result = append(result, value...)
	}
	return result
}
