// Kasera Pay demo shop — the whole backend in one file, Go stdlib only.
//
// It serves the static pages in ./public and exposes three endpoints:
//
//	POST /api/orders      create an order + a Kasera payment request
//	GET  /api/orders/{id} read an order's status (polling case refreshes from Kasera)
//	POST /webhook         receive Kasera's signed payment.paid callback
//
// Read it top to bottom; the code is the documentation.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// The catalog. This server-side table is the ONLY source of prices. The
// browser sends an item_id, never an amount — a client that could name its
// own price would pay Rp 1 for everything. (public/store.js has a display
// copy of this list; if they drift, the server wins.)
// ---------------------------------------------------------------------------

type Item struct {
	ID    string
	Name  string
	Price int64 // whole rupiah — IDR has no cents
}

var items = map[string]Item{
	"tee-batik":     {"tee-batik", "Batik Print Tee", 189000},
	"tee-plain":     {"tee-plain", "Heavyweight Plain Tee", 149000},
	"hoodie-kasera": {"hoodie-kasera", "Kasera Threads Hoodie", 429000},
	"cap-canvas":    {"cap-canvas", "Canvas Cap", 119000},
	"socks-3pack":   {"socks-3pack", "Socks (3 pack)", 89000},
	"tote-denim":    {"tote-denim", "Denim Tote Bag", 159000},
}

// ---------------------------------------------------------------------------
// Orders. An in-memory map is all a demo needs.
// ponytail: map+mutex resets on restart; swap for a real database (or even a
// JSON file) the moment orders must survive a deploy.
// ---------------------------------------------------------------------------

type Order struct {
	ID               string `json:"id"`
	ItemID           string `json:"item_id"`
	ItemName         string `json:"item_name"`
	Amount           int64  `json:"amount"`
	Case             string `json:"case"` // redirect | webhook | polling
	PaymentRequestID string `json:"payment_request_id"`
	MerchantRef      string `json:"merchant_ref"` // our ref, echoed back by Kasera
	CheckoutURL      string `json:"checkout_url"`
	Status           string `json:"status"` // pending | paid | expired
}

var (
	mu     sync.Mutex
	orders = map[string]*Order{}
)

// ---------------------------------------------------------------------------
// Kasera API client. Two calls, both plain HTTP with a bearer key. The key
// lives here, server-side, read from the environment — it can create payment
// requests on your behalf, so it must never reach the browser.
// ---------------------------------------------------------------------------

var (
	apiKey        = os.Getenv("KASERA_API_KEY")
	apiBase       = envOr("KASERA_API_BASE", "http://localhost:8888")
	webhookSecret = os.Getenv("WEBHOOK_SECRET")
)

// paymentRequest is the slice of Kasera's response this demo cares about.
// (The real response has more fields: fee, net, payer, timestamps.)
type paymentRequest struct {
	ID          string `json:"id"`     // "payreq_<uuid>"
	Status      string `json:"status"` // pending | paid | expired | canceled
	CheckoutURL string `json:"checkout_url"`
	MerchantRef string `json:"merchant_ref"` // our order ID, echoed back
}

// createPaymentRequest asks Kasera for a hosted checkout page.
// The Idempotency-Key header is mandatory on this API: if our request times
// out and we retry with the same key, Kasera returns the original payment
// request instead of charging the buyer twice. Our order ID is a perfect key.
func createPaymentRequest(orderID string, it Item) (*paymentRequest, error) {
	body, _ := json.Marshal(map[string]any{
		"amount":      it.Price, // from OUR table, never from the client
		"description": "Kasera Threads — " + it.Name,
		"external_id": orderID, // comes back in the webhook, links it to our order
		// merchant_ref is our own reference, echoed back in every response —
		// and it doubles as the idempotency scope if you skip the header.
		"merchant_ref": orderID,
		// customer + order_items are display detail: the hosted checkout
		// renders the item lines, and Kasera's dashboard shows who bought.
		// Kasera rejects order_items whose Σ price×quantity ≠ amount, so the
		// amount above stays authoritative.
		"customer": map[string]string{"name": "Demo Buyer"},
		"order_items": []map[string]any{
			{"name": it.Name, "price": it.Price, "quantity": 1},
		},
		// return_url would bring the buyer back here after paying:
		//   "return_url": "http://localhost:3300/order.html?order=" + orderID,
		// but Kasera requires https (a payment page never redirects somewhere
		// unencrypted, no dev exception), and this demo serves plain http —
		// so it stays a comment. See README "Redirect-back". order.html
		// already handles the ?order=&status=paid arrival for when you run
		// this demo behind https (e.g. a tunnel).
	})
	req, err := http.NewRequest("POST", apiBase+"/v1/payment-requests", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "order-"+orderID)
	return doKasera(req)
}

// getPaymentRequest reads the current state of a payment request — this is
// the whole of the "polling" integration case.
func getPaymentRequest(id string) (*paymentRequest, error) {
	req, err := http.NewRequest("GET", apiBase+"/v1/payment-requests/"+id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return doKasera(req)
}

func doKasera(req *http.Request) (*paymentRequest, error) {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("kasera answered %d: %s", res.StatusCode, raw)
	}
	var pr paymentRequest
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// handleCreateOrder: the browser says WHAT it wants (item_id) and HOW the
// shop should learn about payment (case); the server decides the price and
// talks to Kasera.
func handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID string `json:"item_id"`
		Case   string `json:"case"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		httpErr(w, 400, "invalid JSON body")
		return
	}
	it, ok := items[in.ItemID]
	if !ok {
		httpErr(w, 404, "unknown item")
		return
	}
	switch in.Case {
	case "redirect", "webhook", "polling":
	default:
		httpErr(w, 400, "case must be redirect, webhook or polling")
		return
	}
	if apiKey == "" {
		httpErr(w, 503, "KASERA_API_KEY is not set — see .env.example")
		return
	}

	o := &Order{
		ID:       randomID(),
		ItemID:   it.ID,
		ItemName: it.Name,
		Amount:   it.Price,
		Case:     in.Case,
		Status:   "pending",
	}
	pr, err := createPaymentRequest(o.ID, it)
	if err != nil {
		log.Printf("create payment request: %v", err)
		httpErr(w, 502, "could not create payment request with Kasera")
		return
	}
	o.PaymentRequestID = pr.ID
	o.MerchantRef = pr.MerchantRef // echoed back by Kasera; equals o.ID
	o.CheckoutURL = pr.CheckoutURL

	mu.Lock()
	orders[o.ID] = o
	mu.Unlock()

	writeJSON(w, 201, o)
}

// handleGetOrder returns an order. For the polling case a pending order is
// refreshed from Kasera first — the shop asks "is it paid yet?" instead of
// waiting to be told (that waiting-to-be-told version is the webhook case).
func handleGetOrder(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	o := orders[r.PathValue("id")]
	mu.Unlock()
	if o == nil {
		httpErr(w, 404, "no such order (note: orders are in-memory and vanish on restart)")
		return
	}

	if o.Case == "polling" && o.Status == "pending" {
		if pr, err := getPaymentRequest(o.PaymentRequestID); err == nil {
			mu.Lock()
			switch pr.Status {
			case "paid":
				o.Status = "paid"
			case "expired", "canceled":
				o.Status = "expired"
			}
			mu.Unlock()
		} else {
			// A failed poll is not a failed order — stay pending, try again
			// on the next poll.
			log.Printf("poll %s: %v", o.PaymentRequestID, err)
		}
	}

	writeJSON(w, 200, o)
}

// handleWebhook receives Kasera's payment.paid event. Three rules:
//
//  1. VERIFY THE SIGNATURE. Anyone on the internet can POST JSON to this
//     URL; only Kasera knows the whsec_ secret. No valid signature, no sale.
//  2. Be idempotent. Delivery is at-least-once, so the same event can
//     arrive twice; marking a paid order paid again is harmless.
//  3. Answer fast with 2xx. Kasera retries non-2xx responses with backoff —
//     do the minimum here and get out.
func handleWebhook(w http.ResponseWriter, r *http.Request) {
	if webhookSecret == "" {
		httpErr(w, 503, "WEBHOOK_SECRET is not set — see .env.example")
		return
	}
	// The signature covers the EXACT body bytes. Read them raw; parse after.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpErr(w, 400, "could not read body")
		return
	}
	if !verifySignature(webhookSecret, body, r.Header.Get("Kasera-Signature")) {
		httpErr(w, 401, "bad signature")
		return
	}

	// Signature checked — NOW the payload can be trusted.
	var ev struct {
		ID   string `json:"id"`   // "evt_..." — dedupe on this in a real system
		Type string `json:"type"` // "payment.paid"
		Data struct {
			PaymentRequestID string `json:"payment_request_id"`
			ExternalID       string `json:"external_id"` // our order ID, echoed back
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		httpErr(w, 400, "invalid JSON")
		return
	}

	if ev.Type == "payment.paid" {
		mu.Lock()
		if o := orders[ev.Data.ExternalID]; o != nil {
			o.Status = "paid" // idempotent: paid stays paid on redelivery
		}
		mu.Unlock()
	}
	// Unknown order or unknown event type still gets a 200: it is not
	// Kasera's problem that our in-memory store forgot (or that we don't
	// handle that event type) — a non-2xx would just make Kasera retry.
	w.WriteHeader(http.StatusOK)
}

// verifySignature checks Kasera's webhook signature: the Kasera-Signature
// header is lowercase hex HMAC-SHA256 over the exact body bytes, keyed with
// your whsec_ secret. hmac.Equal is a constant-time compare — a plain ==
// would leak, byte by byte, how much of a guessed signature was right.
func verifySignature(secret string, body []byte, signature string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// ---------------------------------------------------------------------------
// Plumbing
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b) // never fails on modern Go
	return hex.EncodeToString(b)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv reads KEY=VALUE lines from .env, if present, into the process
// environment. Twelve lines of stdlib instead of a dependency.
func loadDotEnv() {
	raw, err := os.ReadFile(".env")
	if err != nil {
		return // no .env is fine; plain env vars work too
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && os.Getenv(k) == "" {
			os.Setenv(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
}

func main() {
	loadDotEnv()
	// Re-read after .env is loaded (package vars above ran before main).
	apiKey = os.Getenv("KASERA_API_KEY")
	apiBase = envOr("KASERA_API_BASE", "http://localhost:8888")
	webhookSecret = os.Getenv("WEBHOOK_SECRET")

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("public")))
	mux.HandleFunc("POST /api/orders", handleCreateOrder)
	mux.HandleFunc("GET /api/orders/{id}", handleGetOrder)
	mux.HandleFunc("POST /webhook", handleWebhook)

	addr := ":" + envOr("PORT", "3300")
	log.Printf("Kasera Threads demo on http://localhost%s (API base %s, key set: %v, webhook secret set: %v)",
		addr, apiBase, apiKey != "", webhookSecret != "")
	log.Fatal(http.ListenAndServe(addr, mux))
}
