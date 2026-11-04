package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
)

var secretKey []byte

func loadSecretKey() error {
	k, err := hex.DecodeString(os.Getenv("SECRET_KEY"))
	if err != nil || len(k) != 32 {
		return errors.New("SECRET_KEY must be 64 hex chars (32 bytes)")
	}
	secretKey = k
	return nil
}

func encrypt(pt []byte) ([]byte, error) {
	b, err := aes.NewCipher(secretKey)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	n := make([]byte, g.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return g.Seal(n, n, pt, nil), nil
}

func decrypt(ct []byte) ([]byte, error) {
	b, err := aes.NewCipher(secretKey)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	if len(ct) < g.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return g.Open(nil, ct[:g.NonceSize()], ct[g.NonceSize():], nil)
}
