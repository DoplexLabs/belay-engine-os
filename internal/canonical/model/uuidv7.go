package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

func IsCanonicalUUIDv7(value string) bool {
	if len(value) != 36 ||
		value[8] != '-' ||
		value[13] != '-' ||
		value[18] != '-' ||
		value[23] != '-' ||
		value[14] != '7' {
		return false
	}
	switch value[19] {
	case '8', '9', 'a', 'b':
	default:
		return false
	}
	for index := range value {
		switch index {
		case 8, 13, 18, 23:
			continue
		}
		character := value[index]
		if !('0' <= character && character <= '9') &&
			!('a' <= character && character <= 'f') {
			return false
		}
	}
	return true
}

func NewUUIDv7(now time.Time, random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	var value [16]byte
	millis := uint64(now.UnixMilli())
	value[0] = byte(millis >> 40)
	value[1] = byte(millis >> 32)
	value[2] = byte(millis >> 24)
	value[3] = byte(millis >> 16)
	value[4] = byte(millis >> 8)
	value[5] = byte(millis)
	if _, err := io.ReadFull(random, value[6:]); err != nil {
		return "", fmt.Errorf("uuidv7 randomness: %w", err)
	}
	value[6] = value[6]&0x0f | 0x70
	value[8] = value[8]&0x3f | 0x80

	var encoded [36]byte
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded[:]), nil
}
