package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
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

// Case 4: the clock moves one calendar month past where it stands — the
// sandbox clock once it has been advanced, the wall clock before that.
func TestMonthLater(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	if got := monthLater("", now); !got.After(now.AddDate(0, 1, 0)) || got.Before(now.AddDate(0, 1, 0)) {
		t.Fatalf("no clock yet: %v", got)
	}
	moved := monthLater("2026-11-30T00:00:00+07:00", now)
	if moved.Year() != 2026 || moved.Month() != time.December || moved.Month() == time.January {
		t.Fatalf("from a moved clock: %v", moved)
	}
	if !moved.After(now.AddDate(0, 2, 0)) {
		t.Fatalf("a moved clock must step from the clock, not the wall: %v", moved)
	}
}

// Case 4: a subscription.* event is filed by its own id, an invoice.* event
// by the subscription it names, and a redelivery is filed once.
func TestRecordSubscriptionEvent(t *testing.T) {
	mu.Lock()
	members = map[string]*Member{"m1": {ID: "m1", SubscriptionID: "sub_1"}}
	mu.Unlock()

	recordSubscriptionEvent("subscription.activated", "evt_a", "t1", "sub_1", "")
	recordSubscriptionEvent("invoice.issued", "evt_b", "t2", "inv_9", "sub_1")
	recordSubscriptionEvent("invoice.issued", "evt_b", "t2", "inv_9", "sub_1") // redelivery
	recordSubscriptionEvent("invoice.paid", "evt_c", "t3", "inv_x", "sub_other")

	mu.Lock()
	got := members["m1"].Events
	mu.Unlock()
	if len(got) != 2 || got[0].Type != "subscription.activated" || got[1].ID != "evt_b" {
		t.Fatalf("events = %+v", got)
	}
}

// Case 4: the hosted invoice page takes the bare id, not the API's inv_ form.
func TestPayURLDropsThePrefix(t *testing.T) {
	apiBase = "https://pay.example"
	if got := payURL("inv_abc"); got != "https://pay.example/i/abc" {
		t.Fatalf("payURL = %q", got)
	}
	if got := payURL("abc"); got != "https://pay.example/i/abc" {
		t.Fatalf("payURL without prefix = %q", got)
	}
}

// Case 4: a delivery signed under the sandbox endpoint's secret is accepted
// beside the live one; an empty secret never verifies anything.
func TestVerifyAnySignature(t *testing.T) {
	body := []byte(`{"id":"evt_2","type":"invoice.issued"}`)
	sign := func(secret string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return hex.EncodeToString(mac.Sum(nil))
	}
	if !verifyAnySignature(body, sign("whsec_test"), "whsec_live", "whsec_test") {
		t.Fatal("sandbox secret rejected")
	}
	if !verifyAnySignature(body, sign("whsec_live"), "whsec_live", "") {
		t.Fatal("live secret rejected when the sandbox one is unset")
	}
	if verifyAnySignature(body, sign(""), "whsec_live", "") {
		t.Fatal("an empty secret must never verify")
	}
	if verifyAnySignature(body, sign("whsec_other"), "whsec_live", "whsec_test") {
		t.Fatal("unknown secret accepted")
	}
}
