package cmd

import (
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"testing"
)

func TestFormatSignature(t *testing.T) {
	r, _ := rand.Int(rand.Reader, elliptic.P256().Params().N)
	s, _ := rand.Int(rand.Reader, elliptic.P256().Params().N)

	sig := formatSignature(r, s)
	if len(sig) != 64 {
		t.Fatalf("expected 64 bytes signature, got %d", len(sig))
	}

	// Verify r and s can be extracted back
	rExt := new(big.Int).SetBytes(sig[:32])
	sExt := new(big.Int).SetBytes(sig[32:])

	if r.Cmp(rExt) != 0 {
		t.Fatalf("r mismatch: expected %v, got %v", r, rExt)
	}
	if s.Cmp(sExt) != 0 {
		t.Fatalf("s mismatch: expected %v, got %v", s, sExt)
	}
}

func TestAPNsTokensStore(t *testing.T) {
	client := &APNsClient{
		isSandboxToken:    make(map[string]bool),
		isProductionToken: make(map[string]bool),
	}

	client.SetToken("charging", "abc123charging")
	if client.tokens.Charging != "abc123charging" {
		t.Fatalf("expected charging token 'abc123charging', got '%s'", client.tokens.Charging)
	}

	client.SetToken("climate", "xyz789climate")
	if client.tokens.Climate != "xyz789climate" {
		t.Fatalf("expected climate token 'xyz789climate', got '%s'", client.tokens.Climate)
	}
}
