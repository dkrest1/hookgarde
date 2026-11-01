package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"testing"
)

func TestEncryptionBindsScope(t *testing.T) {
	key := make([]byte, 32)
	cipher, e := Encrypt(key, "secret", "organization:project:endpoint:key")
	if e != nil {
		t.Fatal(e)
	}
	plain, e := Decrypt(key, cipher, "organization:project:endpoint:key")
	if e != nil || plain != "secret" {
		t.Fatal(e)
	}
	if _, e = Decrypt(key, cipher, "different-organization"); e == nil {
		t.Fatal("scope substitution accepted")
	}
	cipher[len(cipher)-1] ^= 1
	if _, e = Decrypt(key, cipher, "organization:project:endpoint:key"); e == nil {
		t.Fatal("tampering accepted")
	}
}
func TestPassword(t *testing.T) {
	hash := PasswordHash("long-and-secret-password")
	if !VerifyPassword("long-and-secret-password", hash) || VerifyPassword("wrong", hash) || VerifyPassword("password", "invalid") {
		t.Fatal("password verification failed")
	}
}
func TestSignatureExactBytes(t *testing.T) {
	body := []byte(`{"id":"event"}`)
	key := []byte("12345678901234567890123456789012")
	secret := base64.RawURLEncoding.EncodeToString(key)
	mac := hmac.New(sha256.New, key)
	mac.Write(append([]byte("123."), body...))
	want := "t=123,kid=2,v1=" + hex.EncodeToString(mac.Sum(nil))
	if got, e := Signature(secret, 123, 2, body); e != nil || got != want {
		t.Fatalf("%s", got)
	}
	if got, _ := Signature(secret, 123, 2, append(body, ' ')); got == want {
		t.Fatal("changed bytes had same signature")
	}
}
func TestPublicAddresses(t *testing.T) {
	for _, tc := range []struct {
		ip string
		ok bool
	}{{"8.8.8.8", true}, {"2606:4700:4700::1111", true}, {"127.0.0.1", false}, {"10.0.0.1", false}, {"172.16.1.1", false}, {"169.254.169.254", false}, {"100.64.0.1", false}, {"192.0.2.1", false}, {"::1", false}, {"::ffff:127.0.0.1", false}, {"64:ff9b::a9fe:a9fe", false}, {"2002:7f00:1::", false}, {"fc00::1", false}, {"0.0.0.0", false}, {"224.0.0.1", false}} {
		if got := Public(net.ParseIP(tc.ip)); got != tc.ok {
			t.Errorf("%s: got %v", tc.ip, got)
		}
	}
}
func TestPolicyRejectsMixedDNSAndUnsafeURLs(t *testing.T) {
	p := Policy{Lookup: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}}
	for _, url := range []string{"https://receiver.example/a", "http://receiver.example", "https://user:pass@receiver.example", "https://receiver.example:8443", "https://receiver.example/#fragment"} {
		if e := p.Validate(context.Background(), url); e == nil {
			t.Errorf("accepted %s", url)
		}
	}
	p.DevelopmentHosts = []string{"receiver.example"}
	if e := p.Validate(context.Background(), "http://receiver.example:8080"); e != nil {
		t.Fatal(e)
	}
	if e := p.Validate(context.Background(), "http://evilreceiver.example:8080"); e == nil {
		t.Fatal("allowlist matched suffix")
	}
}
