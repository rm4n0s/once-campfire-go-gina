// Package uuid generates and parses RFC 9562 version 4 UUIDs. It stands in for the
// "uuid" package that only newer Go releases ship in their standard library.
package uuid

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// UUID is a 128-bit identifier.
type UUID [16]byte

// NewV4 returns a random (version 4) UUID.
func NewV4() UUID {
	var u UUID
	if _, err := rand.Read(u[:]); err != nil {
		panic(err)
	}
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return u
}

// String returns the canonical 8-4-4-4-12 form.
func (u UUID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}

// Parse accepts the canonical textual form.
func Parse(s string) (UUID, error) {
	var u UUID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return u, errors.New("invalid UUID")
	}
	raw := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:]
	if _, err := hex.Decode(u[:], []byte(raw)); err != nil {
		return u, errors.New("invalid UUID")
	}
	return u, nil
}
