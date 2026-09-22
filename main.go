// Kasera Pay demo shop — the whole backend in one file, Go stdlib only.
//
// It serves the static pages in ./public and exposes these endpoints:
//
//	POST /api/orders                    create an order + a Kasera transaction
//	GET  /api/orders/{id}               read an order's status (polling case refreshes from Kasera)
//	POST /webhook                       receive Kasera's signed callbacks (payment.paid, subscription.*, invoice.*)
//	POST /api/subscriptions             case 4: a customer + a monthly subscription (sandbox)
//	GET  /api/subscriptions/{id}        the subscription, its invoices, and the webhook events seen for it
//	POST /api/subscriptions/{id}/advance move its sandbox clock one month — the renewal sweep does the rest
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
	"time"
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
	ID       string `json:"id"`
	ItemID   string `json:"item_id"`
	ItemName string `json:"item_name"`
	Amount   int64  `json:"amount"`
	Case     string `json:"case"` // redirect | webhook | polling
	// Kasera's id for the transaction. Still payreq_-prefixed and still named
	// payment_request_id in the webhook payload: the API renamed its routes to
	// /v1/transactions, but ids already written into integrators' databases —
	// and the field names that carry them — kept their old spelling.
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
	// testAPIKey is a SANDBOX key (kp_test_...) for case 4. A subscription is
	// sandbox or live by the key that created it, and only a sandbox one can
	// have its clock moved — so the recurring case needs a test key even where
	// the one-off cases run on a live key. Falls back to KASERA_API_KEY.
	testAPIKey = envOr("KASERA_TEST_API_KEY", os.Getenv("KASERA_API_KEY"))
	// publicURL is this demo's own https origin (e.g. https://demo-pay.kasera.id).
	// When set, orders carry a return_url so the checkout sends the buyer back
	// to the order page after paying. Kasera only accepts https return URLs —
	// a payment page never redirects somewhere unencrypted — so local plain-http
	// runs leave this empty and the redirect-back simply doesn't happen there.
	publicURL = os.Getenv("PUBLIC_URL")
)

// transaction is the slice of Kasera's response this demo cares about.
// (The real response has more fields: fee, net, qris_string, timestamps.)
type transaction struct {
	ID          string `json:"id"`     // "payreq_<uuid>" — prefix predates the rename, kept forever
	Status      string `json:"status"` // pending | succeeded | failed | expired | canceled
	CheckoutURL string `json:"checkout_url"`
	MerchantRef string `json:"merchant_ref"` // our order ID, echoed back
}

// createTransaction asks Kasera for a hosted checkout page.
// The Idempotency-Key header is OPTIONAL — but sending it is the best
// practice this demo teaches: if our request times out and we retry with the
// same key, Kasera returns the original transaction instead of charging the
// buyer twice. Skip it and every retry is its own charge. Our order ID is a
// perfect key. (Only this header deduplicates; merchant_ref and external_id
// are labels, stored and echoed, never a retry key.)
func createTransaction(orderID string, it Item) (*transaction, error) {
	payload := map[string]any{
		"amount":      it.Price, // from OUR table, never from the client
		"description": "Kasera Threads — " + it.Name,
		"external_id": orderID, // comes back in the webhook, links it to our order
		// merchant_ref is our own reference, echoed back in every response.
		// A label only — it never deduplicates anything (see Idempotency-Key).
		"merchant_ref": orderID,
		// customer + order_items are display detail: the hosted checkout
		// renders the item lines, and Kasera's dashboard shows who bought.
		// Kasera rejects order_items whose Σ price×quantity ≠ amount, so the
		// amount above stays authoritative.
		"customer": map[string]string{"name": "Demo Buyer"},
		"order_items": []map[string]any{
			{"name": it.Name, "price": it.Price, "quantity": 1},
		},
	}
	// return_url brings the buyer back to the order page after paying —
	// Kasera appends ?status=succeeded on arrival (display only; the order
	// page never trusts it, see order.html). Kasera accepts https URLs only —
	// a payment page never redirects somewhere unencrypted, no dev exception —
	// so this rides on PUBLIC_URL being set (the deployed demo) and local
	// plain-http runs simply skip the redirect-back.
	if publicURL != "" {
		payload["return_url"] = publicURL + "/order.html?order=" + orderID
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", apiBase+"/v1/transactions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "order-"+orderID)
	return doKasera(req)
}

// getTransaction reads the current state of a transaction — this is the
// whole of the "polling" integration case.
func getTransaction(id string) (*transaction, error) {
	req, err := http.NewRequest("GET", apiBase+"/v1/transactions/"+id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return doKasera(req)
}

func doKasera(req *http.Request) (*transaction, error) {
	var tx transaction
	if _, err := kaseraJSON(req, &tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

// kaseraJSON sends one request and decodes the JSON answer into out. A
// non-2xx is an error carrying the status, so a caller can tell a 404 (the
// subscriptions surface is not enabled for this account) from anything else.
func kaseraJSON(req *http.Request, out any) (int, error) {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return res.StatusCode, fmt.Errorf("kasera answered %d: %s", res.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return res.StatusCode, err
		}
	}
	return res.StatusCode, nil
}

// ---------------------------------------------------------------------------
// Case 4 — a monthly membership, run through a renewal without waiting a
// month. Three Kasera objects: a plan (made once, reused), a customer, a
// subscription. The first invoice is issued at once; every later one is
// issued by Kasera's renewal sweep when the subscription's clock crosses the
// period end. In sandbox that clock can be moved by hand.
// ---------------------------------------------------------------------------

const (
	planCode   = "member-threads"
	planName   = "Member Kasera Threads"
	planAmount = 49000 // Rp 49.000 a month, whole rupiah
)

// The slices of Kasera's subscription objects this demo reads. The real
// responses carry more (trial, quantity, discount, tax…).
type subscription struct {
	ID          string `json:"id"` // "sub_..."
	CustomerID  string `json:"customer_id"`
	PlanID      string `json:"plan_id"`
	Status      string `json:"status"` // pending_first_payment | active | past_due | paused | canceled
	PeriodStart string `json:"current_period_start"`
	PeriodEnd   string `json:"current_period_end"`
	PaidThrough string `json:"paid_through,omitempty"`
	IsTest      bool   `json:"is_test"`
	TestClock   string `json:"test_clock,omitempty"` // where the sandbox clock stands, once moved
}

type invoice struct {
	ID             string `json:"id"` // "inv_..."
	Number         string `json:"number"`
	SubscriptionID string `json:"subscription_id"`
	Status         string `json:"status"` // open | paid | overdue | void | uncollectible
	Total          int64  `json:"total"`
	PeriodStart    string `json:"period_start"`
	PeriodEnd      string `json:"period_end"`
	DueAt          string `json:"due_at"`
	PaidAt         string `json:"paid_at,omitempty"`
	// PayURL is ours, not Kasera's: the hosted invoice page the customer pays
	// on, built from the invoice id.
	PayURL string `json:"pay_url"`
}

// A webhook event as this shop saw it — the part of the stream case 4 shows.
type seenEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	At   string `json:"at"`
}

// Member is what this shop keeps about a subscriber. The subscription and
// its invoices are read fresh from Kasera on every GET — Kasera is the
// source of truth for money, the shop only remembers who signed up.
type Member struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Email          string      `json:"email"`
	CustomerID     string      `json:"customer_id"`
	SubscriptionID string      `json:"subscription_id"`
	Events         []seenEvent `json:"events"`
}

var (
	members = map[string]*Member{} // guarded by mu, like orders
	planID  string                 // cached after the first ensurePlan
)

func kaseraReq(method, path string, payload any) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, apiBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// ensurePlan finds the demo's plan by code, creating it the first time. A
// plan is a price list entry, not a purchase: making it once is the whole
// setup, and the code makes the lookup idempotent across restarts.
func ensurePlan() (string, int, error) {
	mu.Lock()
	cached := planID
	mu.Unlock()
	if cached != "" {
		return cached, 200, nil
	}
	req, err := kaseraReq("GET", "/v1/subscription_plans?limit=100", nil)
	if err != nil {
		return "", 0, err
	}
	var list struct {
		Data []struct {
			ID       string `json:"id"`
			Code     string `json:"code"`
			Archived bool   `json:"archived"`
		} `json:"data"`
	}
	if code, err := kaseraJSON(req, &list); err != nil {
		return "", code, err
	}
	for _, p := range list.Data {
		if p.Code == planCode && !p.Archived {
			mu.Lock()
			planID = p.ID
			mu.Unlock()
			return p.ID, 200, nil
		}
	}
	req, err = kaseraReq("POST", "/v1/subscription_plans", map[string]any{
		"code":           planCode,
		"name":           planName,
		"amount":         planAmount,
		"interval_unit":  "month",
		"interval_count": 1,
	})
	if err != nil {
		return "", 0, err
	}
	var created struct {
		ID string `json:"id"`
	}
	if code, err := kaseraJSON(req, &created); err != nil {
		return "", code, err
	}
	mu.Lock()
	planID = created.ID
	mu.Unlock()
	return created.ID, 201, nil
}

// subscriptionsUnavailable turns Kasera's 404 on the whole surface into the
// one-line explanation the page shows instead of a broken form: the
// subscriptions beta is switched on per merchant account.
func subscriptionsUnavailable(w http.ResponseWriter, code int, err error) bool {
	if code == 404 {
		httpErr(w, 503, "subscriptions are not enabled for this Kasera account yet (the beta is switched on per merchant) — ask Kasera to enable it for the key in KASERA_TEST_API_KEY")
		return true
	}
	return false
}

// handleCreateMember: name + email in, a customer and a monthly subscription
// out. The opening invoice is issued by Kasera at once, with a hosted page to
// pay it on; the subscription activates when it is paid.
func handleCreateMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		httpErr(w, 400, "invalid JSON body")
		return
	}
	in.Name, in.Email = strings.TrimSpace(in.Name), strings.TrimSpace(in.Email)
	if in.Name == "" || !strings.Contains(in.Email, "@") {
		httpErr(w, 400, "name and a valid email are required")
		return
	}
	if testAPIKey == "" {
		httpErr(w, 503, "KASERA_TEST_API_KEY is not set — see .env.example")
		return
	}
	plan, code, err := ensurePlan()
	if err != nil {
		log.Printf("ensure plan: %v", err)
		if !subscriptionsUnavailable(w, code, err) {
			httpErr(w, 502, "could not set up the plan with Kasera")
		}
		return
	}

	m := &Member{ID: randomID(), Name: in.Name, Email: in.Email, Events: []seenEvent{}}

	req, _ := kaseraReq("POST", "/v1/subscription_customers", map[string]any{
		"external_id": m.ID, // our member id, so the customer links back to us
		"name":        in.Name,
		"email":       in.Email,
	})
	var cust struct {
		ID string `json:"id"`
	}
	if code, err := kaseraJSON(req, &cust); err != nil {
		log.Printf("create customer: %v", err)
		if !subscriptionsUnavailable(w, code, err) {
			httpErr(w, 502, "could not create the customer with Kasera")
		}
		return
	}
	m.CustomerID = cust.ID

	req, _ = kaseraReq("POST", "/v1/subscriptions", map[string]any{
		"customer_id": cust.ID,
		"plan_id":     plan,
		"external_id": m.ID,
		// Sandbox by intent, on top of the key: only a sandbox subscription
		// can have its clock moved, and this case is about moving it.
		"is_test": true,
	})
	// Same idempotency practice as the one-off order (see createTransaction).
	req.Header.Set("Idempotency-Key", "member-"+m.ID)
	var sub subscription
	if _, err := kaseraJSON(req, &sub); err != nil {
		log.Printf("create subscription: %v", err)
		httpErr(w, 502, "could not create the subscription with Kasera")
		return
	}
	m.SubscriptionID = sub.ID

	mu.Lock()
	members[m.ID] = m
	mu.Unlock()

	writeJSON(w, 201, memberView(m, &sub))
}

// memberView is the page's whole model: who, the subscription as Kasera
// sees it now, its invoices, and the events this shop's webhook has seen.
func memberView(m *Member, sub *subscription) map[string]any {
	invoices, err := listInvoices(sub.ID)
	if err != nil {
		log.Printf("list invoices %s: %v", sub.ID, err)
	}
	mu.Lock()
	events := append([]seenEvent{}, m.Events...)
	mu.Unlock()
	return map[string]any{
		"member":       m,
		"subscription": sub,
		"invoices":     invoices,
		"events":       events,
	}
}

func getSubscription(id string) (*subscription, error) {
	req, err := kaseraReq("GET", "/v1/subscriptions/"+id, nil)
	if err != nil {
		return nil, err
	}
	var sub subscription
	if _, err := kaseraJSON(req, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

// listInvoices reads the sandbox invoices and keeps the ones for this
// subscription, oldest first. The API lists per account, not per
// subscription; a shop with many members would key its own copy by
// subscription_id from the invoice.* webhooks instead.
func listInvoices(subID string) ([]invoice, error) {
	req, err := kaseraReq("GET", "/v1/subscription_invoices?limit=100", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []invoice `json:"data"`
	}
	if _, err := kaseraJSON(req, &list); err != nil {
		return nil, err
	}
	out := []invoice{}
	for _, inv := range list.Data {
		if inv.SubscriptionID == subID {
			inv.PayURL = payURL(inv.ID)
			out = append(out, inv)
		}
	}
	// Oldest first reads as a timeline; the API answers newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// payURL is the hosted invoice page the customer pays on. It lives on the
// same host as the API and is addressed by the bare id: the API's ids carry
// an inv_ prefix, the page (like the link in Kasera's own reminder emails)
// does not.
func payURL(invoiceID string) string {
	return apiBase + "/i/" + strings.TrimPrefix(invoiceID, "inv_")
}

func handleGetMember(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	m := members[r.PathValue("id")]
	mu.Unlock()
	if m == nil {
		httpErr(w, 404, "no such member (note: members are in-memory and vanish on restart)")
		return
	}
	sub, err := getSubscription(m.SubscriptionID)
	if err != nil {
		log.Printf("read subscription %s: %v", m.SubscriptionID, err)
		httpErr(w, 502, "could not read the subscription from Kasera")
		return
	}
	writeJSON(w, 200, memberView(m, sub))
}

// handleAdvance moves the sandbox clock one month past where it stands.
// This bills nothing by itself: Kasera's renewal sweep then does, on its next
// pass, exactly what it does in production — issues the invoice for the
// cycle crossed and extends access when it is paid. The page keeps reading
// until the new invoice shows up.
func handleAdvance(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	m := members[r.PathValue("id")]
	mu.Unlock()
	if m == nil {
		httpErr(w, 404, "no such member")
		return
	}
	cur, err := getSubscription(m.SubscriptionID)
	if err != nil {
		httpErr(w, 502, "could not read the subscription from Kasera")
		return
	}
	to := monthLater(cur.TestClock, time.Now())
	req, _ := kaseraReq("POST", "/v1/subscriptions/"+m.SubscriptionID+"/advance", map[string]any{
		"to": to.Format(time.RFC3339),
	})
	var sub subscription
	if _, err := kaseraJSON(req, &sub); err != nil {
		log.Printf("advance %s: %v", m.SubscriptionID, err)
		httpErr(w, 502, "Kasera refused to move the clock — only a sandbox subscription can be advanced, and only forward")
		return
	}
	writeJSON(w, 200, memberView(m, &sub))
}

// monthLater is one calendar month past the subscription's clock — which is
// test_clock once it has been moved, and the wall clock before that. One
// extra hour clears the period end whatever the day-of-month arithmetic did.
func monthLater(testClock string, now time.Time) time.Time {
	standing := now
	if t, err := time.Parse(time.RFC3339, testClock); err == nil {
		standing = t
	}
	return standing.AddDate(0, 1, 0).Add(time.Hour)
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
	tx, err := createTransaction(o.ID, it)
	if err != nil {
		log.Printf("create transaction: %v", err)
		httpErr(w, 502, "could not create transaction with Kasera")
		return
	}
	o.PaymentRequestID = tx.ID
	o.MerchantRef = tx.MerchantRef // echoed back by Kasera; equals o.ID
	o.CheckoutURL = tx.CheckoutURL

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
		if tx, err := getTransaction(o.PaymentRequestID); err == nil {
			mu.Lock()
			// Kasera's vocabulary ("succeeded" — one word that reads right on
			// payins and payouts alike) maps onto this shop's simpler one.
			switch tx.Status {
			case "succeeded":
				o.Status = "paid"
			case "expired", "canceled", "failed":
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
		ID        string `json:"id"`   // "evt_..." — dedupe on this in a real system
		Type      string `json:"type"` // "payment.paid", "subscription.*", "invoice.*"
		CreatedAt string `json:"created_at"`
		Data      struct {
			PaymentRequestID string `json:"payment_request_id"`
			ExternalID       string `json:"external_id"` // our order ID, echoed back
			// Case 4: a subscription.* event's data IS the subscription
			// (id sub_...); an invoice.* event's data is the invoice, which
			// names its subscription.
			ID             string `json:"id"`
			SubscriptionID string `json:"subscription_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		httpErr(w, 400, "invalid JSON")
		return
	}

	switch {
	case ev.Type == "payment.paid":
		mu.Lock()
		if o := orders[ev.Data.ExternalID]; o != nil {
			o.Status = "paid" // idempotent: paid stays paid on redelivery
		}
		mu.Unlock()
	case strings.HasPrefix(ev.Type, "subscription.") || strings.HasPrefix(ev.Type, "invoice."):
		recordSubscriptionEvent(ev.Type, ev.ID, ev.CreatedAt, ev.Data.ID, ev.Data.SubscriptionID)
	}
	// Unknown order or unknown event type still gets a 200: it is not
	// Kasera's problem that our in-memory store forgot (or that we don't
	// handle that event type) — a non-2xx would just make Kasera retry.
	w.WriteHeader(http.StatusOK)
}

// recordSubscriptionEvent files a subscription.* or invoice.* event under the
// member it belongs to, so the page can show the stream a real shop would
// react to (activate access on subscription.activated, chase on
// invoice.overdue, revoke on subscription.canceled). Redelivery is harmless:
// the same event id is filed once.
func recordSubscriptionEvent(typ, id, at, dataID, dataSubID string) {
	subID := dataSubID
	if strings.HasPrefix(typ, "subscription.") {
		subID = dataID
	}
	mu.Lock()
	defer mu.Unlock()
	for _, m := range members {
		if m.SubscriptionID != subID {
			continue
		}
		for _, e := range m.Events {
			if e.ID == id {
				return
			}
		}
		m.Events = append(m.Events, seenEvent{ID: id, Type: typ, At: at})
		return
	}
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
	testAPIKey = envOr("KASERA_TEST_API_KEY", apiKey)

	mux := http.NewServeMux()
	// no-cache means "revalidate before using", not "don't cache": the browser
	// still gets 304s, but never runs yesterday's store.js under today's
	// index.html — which is exactly how the theme button once shipped blank.
	static := http.FileServer(http.Dir("public"))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
	mux.HandleFunc("POST /api/orders", handleCreateOrder)
	mux.HandleFunc("GET /api/orders/{id}", handleGetOrder)
	mux.HandleFunc("POST /webhook", handleWebhook)
	// Case 4 — subscriptions (sandbox).
	mux.HandleFunc("POST /api/subscriptions", handleCreateMember)
	mux.HandleFunc("GET /api/subscriptions/{id}", handleGetMember)
	mux.HandleFunc("POST /api/subscriptions/{id}/advance", handleAdvance)

	addr := ":" + envOr("PORT", "3300")
	log.Printf("Kasera Threads demo on http://localhost%s (API base %s, key set: %v, sandbox key set: %v, webhook secret set: %v)",
		addr, apiBase, apiKey != "", strings.HasPrefix(testAPIKey, "kp_test_"), webhookSecret != "")
	log.Fatal(http.ListenAndServe(addr, mux))
}
