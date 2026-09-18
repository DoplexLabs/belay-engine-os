package local

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestFixActionTokenRoundTripTamperingAndStoreBinding(t *testing.T) {
	store := openStorageTestStore(t)
	_, issueID, err := store.DeriveIssueIdentity(
		"failure.v1",
		"explicit_command_failure",
		"scope-token",
		"dimension",
	)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC)
	claims := model.FixActionClaims{
		Version:             model.FixActionTokenVersion,
		CursorEpoch:         mustIssueCursorEpoch(t, store),
		IssueID:             issueID,
		Snapshot:            17,
		RetentionGeneration: mustRetentionGeneration(t, store),
		IssuedAt:            issuedAt,
		ExpiresAt:           issuedAt.Add(issueCursorLifetime),
	}
	token, err := store.IssueFixActionToken(claims)
	if err != nil {
		t.Fatalf("IssueFixActionToken() error = %v", err)
	}
	decoded, err := store.DecodeFixActionToken(token)
	if err != nil {
		t.Fatalf("DecodeFixActionToken() error = %v", err)
	}
	if decoded != claims {
		t.Fatalf("decoded claims = %+v, want %+v", decoded, claims)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("token parts = %d, want 2", len(parts))
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(body) == 0 {
		t.Fatalf("decode token body = %d bytes, %v", len(body), err)
	}
	body[0] ^= 0x01
	tamperedBody := base64.RawURLEncoding.EncodeToString(body)
	if tamperedBody == parts[0] {
		t.Fatal("tampered token body did not change")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) == 0 {
		t.Fatalf("decode token signature = %d bytes, %v", len(signature), err)
	}
	signature[0] ^= 0x01
	tamperedSignature := base64.RawURLEncoding.EncodeToString(signature)
	if tamperedSignature == parts[1] {
		t.Fatal("tampered token signature did not change")
	}
	for _, invalid := range []string{
		tamperedBody + "." + parts[1],
		parts[0] + "." + tamperedSignature,
		token + ".extra",
		"",
	} {
		if _, err := store.DecodeFixActionToken(invalid); !errors.Is(err, ErrFixActionTokenInvalid) {
			t.Errorf("DecodeFixActionToken(tampered) error = %v", err)
		}
	}
	if _, err := openStorageTestStore(t).DecodeFixActionToken(token); !errors.Is(
		err,
		ErrFixActionTokenInvalid,
	) {
		t.Fatalf("token from another store error = %v", err)
	}
}

func TestFixActionTokenV1AndStaleV2EpochAreExpired(t *testing.T) {
	store := openStorageTestStore(t)
	_, issueID, err := store.DeriveIssueIdentity("v1", "detector", "scope", "dimension")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	key, err := store.derivedKey(fixActionTokenKeyDomain)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(key)
	seal := func(body []byte) string {
		return base64.RawURLEncoding.EncodeToString(body) + "." +
			base64.RawURLEncoding.EncodeToString(opaqueDigest(key, body))
	}

	legacyBody, err := json.Marshal(fixActionTokenPayloadV1{
		Version:   model.FixActionTokenVersionV1,
		IssueID:   issueID,
		Snapshot:  1,
		IssuedAt:  formatProjectionTime(base),
		ExpiresAt: formatProjectionTime(base.Add(time.Minute)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecodeFixActionToken(seal(legacyBody)); !errors.Is(
		err,
		ErrFixActionTokenExpired,
	) {
		t.Fatalf("signed V1 error = %v", err)
	}
	for name, payload := range map[string]fixActionTokenPayloadV1{
		"invalid issue": {
			Version: model.FixActionTokenVersionV1, IssueID: "iss_invalid",
			Snapshot: 1, IssuedAt: formatProjectionTime(base),
			ExpiresAt: formatProjectionTime(base.Add(time.Minute)),
		},
		"nonpositive snapshot": {
			Version: model.FixActionTokenVersionV1, IssueID: issueID,
			Snapshot: 0, IssuedAt: formatProjectionTime(base),
			ExpiresAt: formatProjectionTime(base.Add(time.Minute)),
		},
		"invalid issued timestamp": {
			Version: model.FixActionTokenVersionV1, IssueID: issueID,
			Snapshot: 1, IssuedAt: "not-a-time",
			ExpiresAt: formatProjectionTime(base.Add(time.Minute)),
		},
		"invalid expiry timestamp": {
			Version: model.FixActionTokenVersionV1, IssueID: issueID,
			Snapshot: 1, IssuedAt: formatProjectionTime(base),
			ExpiresAt: "not-a-time",
		},
		"nonincreasing expiry": {
			Version: model.FixActionTokenVersionV1, IssueID: issueID,
			Snapshot: 1, IssuedAt: formatProjectionTime(base),
			ExpiresAt: formatProjectionTime(base),
		},
		"excessive lifetime": {
			Version: model.FixActionTokenVersionV1, IssueID: issueID,
			Snapshot: 1, IssuedAt: formatProjectionTime(base),
			ExpiresAt: formatProjectionTime(
				base.Add(issueCursorLifetime + time.Nanosecond),
			),
		},
	} {
		t.Run("malformed signed V1 "+name, func(t *testing.T) {
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.DecodeFixActionToken(seal(body)); !errors.Is(
				err,
				ErrFixActionTokenInvalid,
			) {
				t.Fatalf("signed malformed V1 error = %v", err)
			}
		})
	}

	staleBody, err := json.Marshal(fixActionTokenPayload{
		Version:             model.FixActionTokenVersion,
		CursorEpoch:         "ice_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IssueID:             issueID,
		Snapshot:            1,
		RetentionGeneration: mustRetentionGeneration(t, store),
		IssuedAt:            formatProjectionTime(base),
		ExpiresAt:           formatProjectionTime(base.Add(time.Minute)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecodeFixActionToken(seal(staleBody)); !errors.Is(
		err,
		ErrFixActionTokenExpired,
	) {
		t.Fatalf("stale V2 epoch error = %v", err)
	}
}

func TestFixActionTokenValidatesStructureButNotCurrentExpiry(t *testing.T) {
	store := openStorageTestStore(t)
	_, issueID, err := store.DeriveIssueIdentity("v1", "detector", "scope", "dimension")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	expiredClaims := model.FixActionClaims{
		Version:             model.FixActionTokenVersion,
		CursorEpoch:         mustIssueCursorEpoch(t, store),
		IssueID:             issueID,
		Snapshot:            1,
		RetentionGeneration: mustRetentionGeneration(t, store),
		IssuedAt:            base,
		ExpiresAt:           base.Add(time.Minute),
	}
	token, err := store.IssueFixActionToken(expiredClaims)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := store.DecodeFixActionToken(token); err != nil || decoded != expiredClaims {
		t.Fatalf("expired structural decode = (%+v, %v)", decoded, err)
	}

	for _, claims := range []model.FixActionClaims{
		{},
		{
			Version:             "wrong",
			CursorEpoch:         expiredClaims.CursorEpoch,
			IssueID:             issueID,
			Snapshot:            1,
			RetentionGeneration: expiredClaims.RetentionGeneration,
			IssuedAt:            base,
			ExpiresAt:           base.Add(time.Minute),
		},
		{
			Version:             model.FixActionTokenVersion,
			CursorEpoch:         expiredClaims.CursorEpoch,
			IssueID:             "iss_invalid",
			Snapshot:            1,
			RetentionGeneration: expiredClaims.RetentionGeneration,
			IssuedAt:            base,
			ExpiresAt:           base.Add(time.Minute),
		},
		{
			Version:             model.FixActionTokenVersion,
			CursorEpoch:         expiredClaims.CursorEpoch,
			IssueID:             issueID,
			Snapshot:            1,
			RetentionGeneration: expiredClaims.RetentionGeneration,
			IssuedAt:            base,
			ExpiresAt:           base.Add(issueCursorLifetime + time.Nanosecond),
		},
	} {
		if _, err := store.IssueFixActionToken(claims); !errors.Is(
			err,
			ErrFixActionTokenInvalid,
		) {
			t.Errorf("IssueFixActionToken(%+v) error = %v", claims, err)
		}
	}
}

func TestFixActionTokenRequiresCurrentMaterializedGeneration(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	_, issueID, err := store.DeriveIssueIdentity(
		"v1", "detector", "scope", "dimension",
	)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	claims := model.FixActionClaims{
		Version:             model.FixActionTokenVersion,
		CursorEpoch:         mustIssueCursorEpoch(t, store),
		IssueID:             issueID,
		Snapshot:            1,
		RetentionGeneration: mustRetentionGeneration(t, store),
		IssuedAt:            base,
		ExpiresAt:           base.Add(time.Minute),
	}
	token, err := store.IssueFixActionToken(claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE issue_projection_metadata
		SET current_generation = current_generation + 1
		WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.IssueFixActionToken(claims); !errors.Is(
		err,
		model.ErrIssueSnapshotExpired,
	) {
		t.Fatalf("issue token during materialization drift error = %v", err)
	}
	if _, err := store.DecodeFixActionToken(token); !errors.Is(
		err,
		model.ErrIssueSnapshotExpired,
	) {
		t.Fatalf("decode token during materialization drift error = %v", err)
	}
}
