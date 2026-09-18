package local

import (
	"bytes"
	"testing"
)

func TestPayloadCipherRoundTripNonceUniquenessAndAuthenticatedContext(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	cipher, err := newPayloadCipher(key, "store_test", &incrementingReader{})
	if err != nil {
		t.Fatalf("newPayloadCipher() error = %v", err)
	}
	defer cipher.close()
	plaintext := []byte(`{"summary":"ENCRYPTION_ROUND_TRIP_CANARY"}`)

	first, err := cipher.seal("event", "event-1", "canonical_json", plaintext)
	if err != nil {
		t.Fatalf("first seal() error = %v", err)
	}
	second, err := cipher.seal("event", "event-1", "canonical_json", plaintext)
	if err != nil {
		t.Fatalf("second seal() error = %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions reused the same nonce/ciphertext")
	}
	if bytes.Contains(first, plaintext) || bytes.Contains(second, plaintext) {
		t.Fatal("ciphertext envelope contains plaintext")
	}

	roundTrip, err := cipher.open(
		"event",
		"event-1",
		"canonical_json",
		payloadEncodingAESGCM,
		first,
	)
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	if !bytes.Equal(roundTrip, plaintext) {
		t.Fatalf("round trip = %q, want %q", roundTrip, plaintext)
	}
	if _, err := cipher.open(
		"event",
		"event-2",
		"canonical_json",
		payloadEncodingAESGCM,
		first,
	); err == nil {
		t.Fatal("authenticated context accepted the wrong record ID")
	}
	if _, err := cipher.open(
		"finding",
		"event-1",
		"canonical_json",
		payloadEncodingAESGCM,
		first,
	); err == nil {
		t.Fatal("authenticated context accepted the wrong record type")
	}
}

type incrementingReader struct {
	next byte
}

func (reader *incrementingReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		reader.next++
		buffer[index] = reader.next
	}
	return len(buffer), nil
}
