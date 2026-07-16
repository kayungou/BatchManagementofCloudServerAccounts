package security

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewRequiresAES256Key(t *testing.T) {
	for _, size := range []int{0, 16, 24, 31, 33} {
		if _, err := New(make([]byte, size)); err == nil {
			t.Fatalf("New accepted a %d-byte key", size)
		}
	}

	key := bytes.Repeat([]byte{0x42}, 32)
	manager, err := New(key)
	if err != nil {
		t.Fatalf("New returned an error for a 32-byte key: %v", err)
	}
	key[0] ^= 0xff

	ciphertext, nonce, err := manager.Encrypt([]byte("secret"), "account:1")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plaintext, err := manager.Decrypt(ciphertext, nonce, "account:1")
	if err != nil {
		t.Fatalf("Decrypt after caller mutated key: %v", err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("Decrypt returned %q, want secret", plaintext)
	}
}

func TestEncryptDecryptAuthenticatesCiphertextAndAAD(t *testing.T) {
	manager, err := New(bytes.Repeat([]byte{0x19}, 32))
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("do-token-value")
	ciphertext, nonce, err := manager.Encrypt(plaintext, "cloud-account:abc")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext contains the plaintext unchanged")
	}

	decrypted, err := manager.Decrypt(ciphertext, nonce, "cloud-account:abc")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("Decrypt returned %q, want %q", decrypted, plaintext)
	}

	if _, err := manager.Decrypt(ciphertext, nonce, "cloud-account:other"); err == nil {
		t.Fatal("Decrypt accepted the wrong associated data")
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := manager.Decrypt(tampered, nonce, "cloud-account:abc"); err == nil {
		t.Fatal("Decrypt accepted tampered ciphertext")
	}
	if _, err := manager.Decrypt(ciphertext, nonce[:len(nonce)-1], "cloud-account:abc"); err == nil {
		t.Fatal("Decrypt accepted a nonce with the wrong length")
	}

	_, secondNonce, err := manager.Encrypt(plaintext, "cloud-account:abc")
	if err != nil {
		t.Fatalf("second Encrypt: %v", err)
	}
	if bytes.Equal(nonce, secondNonce) {
		t.Fatal("two encryptions unexpectedly reused a GCM nonce")
	}
}

func TestFingerprintIsDeterministicAndKeyed(t *testing.T) {
	one, _ := New(bytes.Repeat([]byte{0x01}, 32))
	two, _ := New(bytes.Repeat([]byte{0x02}, 32))

	first := one.Fingerprint("dop_v1_token")
	if !bytes.Equal(first, one.Fingerprint("dop_v1_token")) {
		t.Fatal("Fingerprint is not deterministic")
	}
	if bytes.Equal(first, one.Fingerprint("another-token")) {
		t.Fatal("different values have the same fingerprint")
	}
	if bytes.Equal(first, two.Fingerprint("dop_v1_token")) {
		t.Fatal("fingerprints are not bound to the manager key")
	}
}

func TestPasswordHashAndVerification(t *testing.T) {
	const password = "a-valid-password-123"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected password hash format: %q", hash)
	}
	if !VerifyPassword(hash, password) {
		t.Fatal("VerifyPassword rejected the correct password")
	}
	if VerifyPassword(hash, "incorrect-password") {
		t.Fatal("VerifyPassword accepted an incorrect password")
	}

	secondHash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("second HashPassword: %v", err)
	}
	if hash == secondHash {
		t.Fatal("password hashes reused a salt")
	}

	for _, malformed := range []string{
		"",
		"not-an-argon-hash",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=bad,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$%%%$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$%%%",
	} {
		if VerifyPassword(malformed, password) {
			t.Fatalf("VerifyPassword accepted malformed hash %q", malformed)
		}
	}
}

func TestHashPasswordLengthLimits(t *testing.T) {
	for _, password := range []string{strings.Repeat("a", 11), strings.Repeat("a", 129)} {
		if _, err := HashPassword(password); err == nil {
			t.Fatalf("HashPassword accepted password with %d bytes", len(password))
		}
	}
	for _, password := range []string{strings.Repeat("a", 12), strings.Repeat("a", 128)} {
		if _, err := HashPassword(password); err != nil {
			t.Fatalf("HashPassword rejected password with %d bytes: %v", len(password), err)
		}
	}
}

func TestGenerateRootPassword(t *testing.T) {
	const allowed = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#%+=_-"

	for _, requested := range []int{0, 12, 20, 48} {
		password, err := GenerateRootPassword(requested)
		if err != nil {
			t.Fatalf("GenerateRootPassword(%d): %v", requested, err)
		}
		wantLength := requested
		if wantLength < 20 {
			wantLength = 20
		}
		if len(password) != wantLength {
			t.Fatalf("GenerateRootPassword(%d) returned length %d, want %d", requested, len(password), wantLength)
		}
		for _, character := range password {
			if !strings.ContainsRune(allowed, character) {
				t.Fatalf("generated password contains disallowed character %q", character)
			}
		}
	}
}

func TestRandomTokenIsURLSafeAndHasRequestedEntropyLength(t *testing.T) {
	token, err := RandomToken(32)
	if err != nil {
		t.Fatalf("RandomToken: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("RandomToken returned invalid base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("RandomToken decoded to %d bytes, want 32", len(decoded))
	}
	if bytes.Equal(HashToken(token), HashToken(token+"x")) {
		t.Fatal("HashToken did not change when the token changed")
	}
}
