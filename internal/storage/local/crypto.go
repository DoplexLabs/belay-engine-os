package local

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	payloadEncodingPlaintext = "plaintext.v0"
	payloadEncodingAESGCM    = "aes256gcm.v1"
	payloadEnvelopeVersion   = byte(1)
)

var payloadEnvelopeMagic = [8]byte{'B', 'L', 'Y', 'P', 'A', 'Y', 'L', 'D'}

type payloadCipher struct {
	aead     cipher.AEAD
	random   io.Reader
	storeID  string
	keyBytes []byte
}

func newPayloadCipher(key []byte, storeID string, random io.Reader) (*payloadCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("local data key must be 32 bytes")
	}
	if storeID == "" {
		return nil, errors.New("local store ID is required")
	}
	if random == nil {
		random = rand.Reader
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("initialize local payload cipher")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("initialize local payload encryption")
	}
	return &payloadCipher{
		aead:     aead,
		random:   random,
		storeID:  storeID,
		keyBytes: append([]byte(nil), key...),
	}, nil
}

func (c *payloadCipher) close() {
	for index := range c.keyBytes {
		c.keyBytes[index] = 0
	}
}

func (c *payloadCipher) seal(recordType, recordID, column string, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return nil, errors.New("generate local payload nonce")
	}
	headerLength := len(payloadEnvelopeMagic) + 1 + 2 + len(nonce)
	envelope := make([]byte, headerLength, headerLength+len(plaintext)+c.aead.Overhead())
	copy(envelope, payloadEnvelopeMagic[:])
	envelope[len(payloadEnvelopeMagic)] = payloadEnvelopeVersion
	binary.BigEndian.PutUint16(
		envelope[len(payloadEnvelopeMagic)+1:],
		uint16(len(nonce)),
	)
	copy(envelope[len(payloadEnvelopeMagic)+3:], nonce)
	return c.aead.Seal(
		envelope,
		nonce,
		plaintext,
		c.authenticatedContext(recordType, recordID, column),
	), nil
}

func (c *payloadCipher) open(recordType, recordID, column, encoding string, envelope []byte) ([]byte, error) {
	if encoding != payloadEncodingAESGCM {
		return nil, fmt.Errorf("unsupported local payload encoding %q", encoding)
	}
	minimumLength := len(payloadEnvelopeMagic) + 1 + 2 + c.aead.NonceSize() + c.aead.Overhead()
	if len(envelope) < minimumLength {
		return nil, errors.New("invalid local payload envelope")
	}
	if string(envelope[:len(payloadEnvelopeMagic)]) != string(payloadEnvelopeMagic[:]) {
		return nil, errors.New("invalid local payload envelope")
	}
	if envelope[len(payloadEnvelopeMagic)] != payloadEnvelopeVersion {
		return nil, errors.New("unsupported local payload envelope version")
	}
	nonceLength := int(binary.BigEndian.Uint16(envelope[len(payloadEnvelopeMagic)+1:]))
	if nonceLength != c.aead.NonceSize() {
		return nil, errors.New("invalid local payload nonce")
	}
	ciphertextOffset := len(payloadEnvelopeMagic) + 3 + nonceLength
	if ciphertextOffset+c.aead.Overhead() > len(envelope) {
		return nil, errors.New("invalid local payload envelope")
	}
	nonce := envelope[len(payloadEnvelopeMagic)+3 : ciphertextOffset]
	plaintext, err := c.aead.Open(
		nil,
		nonce,
		envelope[ciphertextOffset:],
		c.authenticatedContext(recordType, recordID, column),
	)
	if err != nil {
		return nil, errors.New("decrypt local payload")
	}
	return plaintext, nil
}

func (c *payloadCipher) authenticatedContext(recordType, recordID, column string) []byte {
	return []byte(
		"belay.local.payload.v1\x00" +
			c.storeID + "\x00" +
			recordType + "\x00" +
			recordID + "\x00" +
			column,
	)
}
