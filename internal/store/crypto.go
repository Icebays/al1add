// Package store keeps every module's configuration on disk in Al1add's own
// encrypted format (.alcf).
//
// File layout (all integers big endian):
//
//	"ALCF" | version(1)=0x01 | nonce(12) | AES-256-GCM ciphertext+tag
//
// The AAD of every file is its module name, so files cannot be swapped
// between modules without detection.
//
// The 32-byte master key lives in master.key:
//
//	"ALMK" | 0x00 | key(32)                                   (plain)
//	"ALMK" | 0x01 | salt(16) | nonce(12) | GCM(key)(48)      (passphrase wrapped)
//
// When a passphrase is used, the wrapping key is PBKDF2-SHA256(passphrase,
// salt, 210000 iterations). A backup zip therefore stays portable: copy the
// .alcf files together with master.key and supply the same passphrase.
package store

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	fileMagic   = "ALCF"
	keyMagic    = "ALMK"
	fileVersion = 1
	pbkdfIter   = 210000
)

// ErrBadPassphrase is returned when master.key cannot be unwrapped.
var ErrBadPassphrase = errors.New("master key: wrong passphrase or corrupted key file")

// Cipher encrypts and decrypts .alcf payloads with the master key.
type Cipher struct {
	aead cipher.AEAD
	raw  []byte
}

func newCipher(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, raw: append([]byte(nil), key...)}, nil
}

// Seal encrypts plaintext for the given module name.
func (c *Cipher) Seal(name string, plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 5+len(nonce)+len(plain)+c.aead.Overhead())
	out = append(out, fileMagic...)
	out = append(out, fileVersion)
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, plain, []byte(name)), nil
}

// Open decrypts an .alcf payload for the given module name.
func (c *Cipher) Open(name string, data []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(data) < 5+ns+c.aead.Overhead() || string(data[:4]) != fileMagic {
		return nil, errors.New("not an .alcf file")
	}
	if data[4] != fileVersion {
		return nil, fmt.Errorf("unsupported .alcf version %d", data[4])
	}
	nonce := data[5 : 5+ns]
	plain, err := c.aead.Open(nil, nonce, data[5+ns:], []byte(name))
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", name, err)
	}
	return plain, nil
}

func deriveWrapKey(passphrase string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, passphrase, salt, pbkdfIter, 32)
}

// encodeMasterKey serialises key, optionally wrapped by passphrase.
func encodeMasterKey(key []byte, passphrase string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(keyMagic)
	if passphrase == "" {
		buf.WriteByte(0)
		buf.Write(key)
		return buf.Bytes(), nil
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	wk, err := deriveWrapKey(passphrase, salt)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(wk)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	buf.WriteByte(1)
	buf.Write(salt)
	buf.Write(nonce)
	buf.Write(aead.Seal(nil, nonce, key, []byte(keyMagic)))
	return buf.Bytes(), nil
}

// decodeMasterKey parses master.key content.
func decodeMasterKey(data []byte, passphrase string) ([]byte, error) {
	if len(data) < 5 || string(data[:4]) != keyMagic {
		return nil, errors.New("master.key: bad format")
	}
	switch data[4] {
	case 0:
		if len(data) != 5+32 {
			return nil, errors.New("master.key: bad length")
		}
		return append([]byte(nil), data[5:]...), nil
	case 1:
		if passphrase == "" {
			return nil, errors.New("master.key is passphrase protected: set AL1ADD_PASSPHRASE or -passphrase")
		}
		if len(data) < 5+16+12+48 {
			return nil, errors.New("master.key: bad length")
		}
		salt := data[5:21]
		nonce := data[21:33]
		wk, err := deriveWrapKey(passphrase, salt)
		if err != nil {
			return nil, err
		}
		block, _ := aes.NewCipher(wk)
		aead, _ := cipher.NewGCM(block)
		key, err := aead.Open(nil, nonce, data[33:], []byte(keyMagic))
		if err != nil {
			return nil, ErrBadPassphrase
		}
		return key, nil
	}
	return nil, fmt.Errorf("master.key: unknown mode %d", data[4])
}

// LoadOrCreateKey opens dir/master.key, creating a random one if missing.
// If the key exists in plain form and a passphrase is now supplied, the key
// file is re-wrapped with the passphrase.
func LoadOrCreateKey(dir, passphrase string) (*Cipher, error) {
	path := filepath.Join(dir, "master.key")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		enc, err := encodeMasterKey(key, passphrase)
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(path, enc, 0o600); err != nil {
			return nil, err
		}
		return newCipher(key)
	}
	if err != nil {
		return nil, err
	}
	key, err := decodeMasterKey(data, passphrase)
	if err != nil {
		return nil, err
	}
	if passphrase != "" && data[4] == 0 {
		if enc, err := encodeMasterKey(key, passphrase); err == nil {
			_ = writeFileAtomic(path, enc, 0o600)
		}
	}
	return newCipher(key)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
