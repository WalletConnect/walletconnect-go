// Package crypto implements the Sign v2 key agreement, topic derivation and
// type 0 envelope encryption.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	KeyLength = 32
	ivLength  = 12

	typeLength = 1
	type0      = 0
)

var (
	ErrUnsupportedEnvelope = errors.New("crypto: unsupported envelope type")
	ErrMalformedEnvelope   = errors.New("crypto: malformed envelope")
)

type Key = [KeyLength]byte

type KeyPair struct {
	Private Key
	Public  Key
}

func RandomKey() (Key, error) {
	var k Key
	if _, err := io.ReadFull(rand.Reader, k[:]); err != nil {
		return Key{}, err
	}
	return k, nil
}

func GenerateKeyPair() (KeyPair, error) {
	priv, err := RandomKey()
	if err != nil {
		return KeyPair{}, err
	}
	pub, err := PublicKey(priv)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Private: priv, Public: pub}, nil
}

func PublicKey(priv Key) (Key, error) {
	out, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return Key{}, err
	}
	var pub Key
	copy(pub[:], out)
	return pub, nil
}

// DeriveSymKey computes X25519(priv, peerPub) and expands it with
// HKDF-SHA256 using no salt and no info, as the JS SDK does.
func DeriveSymKey(priv, peerPub Key) (Key, error) {
	shared, err := curve25519.X25519(priv[:], peerPub[:])
	if err != nil {
		return Key{}, err
	}
	var sym Key
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, nil, nil), sym[:]); err != nil {
		return Key{}, err
	}
	return sym, nil
}

// Topic is the hex SHA-256 of the raw sym key bytes.
func Topic(symKey Key) string {
	sum := sha256.Sum256(symKey[:])
	return hex.EncodeToString(sum[:])
}

// HashMessage is the hex SHA-256 of the UTF-8 string.
func HashMessage(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func EncodeKey(k Key) string { return hex.EncodeToString(k[:]) }

func DecodeKey(s string) (Key, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return Key{}, fmt.Errorf("crypto: invalid hex key: %w", err)
	}
	if len(b) != KeyLength {
		return Key{}, fmt.Errorf("crypto: key must be %d bytes, got %d", KeyLength, len(b))
	}
	var k Key
	copy(k[:], b)
	return k, nil
}

// Encrypt seals plaintext into a base64 type 0 envelope with a random IV.
func Encrypt(symKey Key, plaintext []byte) (string, error) {
	iv := make([]byte, ivLength)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}
	return EncryptWithIV(symKey, plaintext, iv)
}

func EncryptWithIV(symKey Key, plaintext, iv []byte) (string, error) {
	if len(iv) != ivLength {
		return "", fmt.Errorf("crypto: iv must be %d bytes", ivLength)
	}
	aead, err := chacha20poly1305.New(symKey[:])
	if err != nil {
		return "", err
	}
	out := make([]byte, 0, typeLength+ivLength+len(plaintext)+aead.Overhead())
	out = append(out, type0)
	out = append(out, iv...)
	out = aead.Seal(out, iv, plaintext, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt opens a base64 type 0 envelope. Type 1 and 2 envelopes are only
// used by the authentication flow and return ErrUnsupportedEnvelope.
func Decrypt(symKey Key, message string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(message)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(message); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedEnvelope, err)
		}
	}
	if len(b) < typeLength {
		return nil, ErrMalformedEnvelope
	}
	if b[0] != type0 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedEnvelope, b[0])
	}
	if len(b) < typeLength+ivLength+chacha20poly1305.Overhead {
		return nil, ErrMalformedEnvelope
	}
	aead, err := chacha20poly1305.New(symKey[:])
	if err != nil {
		return nil, err
	}
	iv := b[typeLength : typeLength+ivLength]
	plain, err := aead.Open(nil, iv, b[typeLength+ivLength:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedEnvelope, err)
	}
	return plain, nil
}
