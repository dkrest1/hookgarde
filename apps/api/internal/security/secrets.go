// Package security owns credentials, authenticated encryption and outbound policy.
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

func Random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
func EqualHash(secret string, hash []byte) bool {
	return subtle.ConstantTimeCompare(Hash(secret), hash) == 1
}
func PasswordHash(password string) string {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		panic(e)
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return "$argon2id$v=19$m=65536,t=3,p=2$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
}
func VerifyPassword(password, encoded string) bool {
	p := strings.Split(encoded, "$")
	if len(p) != 6 || p[1] != "argon2id" || p[2] != "v=19" || p[3] != "m=65536,t=3,p=2" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(p[4])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(p[5])
	if e != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}
func Encrypt(key []byte, secret, associated string) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return a.Seal(nonce, nonce, []byte(secret), []byte(associated)), nil
}
func Decrypt(key, ciphertext []byte, associated string) (string, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	if len(ciphertext) < a.NonceSize() {
		return "", fmt.Errorf("invalid ciphertext")
	}
	b, e := a.Open(nil, ciphertext[:a.NonceSize()], ciphertext[a.NonceSize():], []byte(associated))
	return string(b), e
}
func Signature(secret string, ts int64, version int, body []byte) (string, error) {
	key, e := base64.RawURLEncoding.DecodeString(secret)
	if e != nil || len(key) != 32 {
		return "", fmt.Errorf("signing secret must encode 32 bytes")
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	m.Write(body)
	return fmt.Sprintf("t=%d,kid=%d,v1=%s", ts, version, hex.EncodeToString(m.Sum(nil))), nil
}
func CSRF(secret, session string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("csrf:" + session))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
