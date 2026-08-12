package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The one security-critical path in this demo: webhook signature
// verification. If this breaks, anyone can POST "your order is paid".
func TestVerifySignature(t *testing.T) {
	secret := "whsec_test_secret"
	body := []byte(`{"id":"evt_1","type":"payment.paid","data":{"external_id":"abc"}}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(secret, body, good) {
		t.Fatal("valid signature rejected")
	}
	if verifySignature(secret, body, "deadbeef") {
		t.Fatal("garbage signature accepted")
	}
	if verifySignature(secret, []byte(`{"tampered":true}`), good) {
		t.Fatal("signature accepted for a different body")
	}
	if verifySignature("whsec_wrong_secret", body, good) {
		t.Fatal("signature accepted under the wrong secret")
	}
	if verifySignature(secret, body, "") {
		t.Fatal("empty signature accepted")
	}
}
