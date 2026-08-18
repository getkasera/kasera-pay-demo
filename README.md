# Kasera Pay demo — four ways to get paid

A tiny clothing shop ("Kasera Threads") that integrates with
[Kasera Pay](https://pay.kasera.id) four different ways, from *no code at
all* to a production-style webhook flow. It is deliberately minimal:

- **Backend:** one Go file, standard library only. No frameworks, no deps.
- **Frontend:** plain HTML + vanilla JS. No build step, no npm.
- The code **is** the documentation — read `main.go` top to bottom.

## The four cases

| # | Case | How the shop learns about payment |
|---|------|-----------------------------------|
| 0 | [No code](public/nonapi.html) | It doesn't need to — you create payment links in the dashboard and share them (WhatsApp, invoice, printed QRIS). |
| 1 | Redirect only | It doesn't (on purpose). Backend creates a payment request, buyer is redirected to checkout — and after paying, Kasera's `return_url` can send them back to `order.html?status=succeeded`. But a query param proves nothing: the shop backend still never learns about the payment. Shows *why* cases 2 and 3 exist. |
| 2 | Webhook | Kasera POSTs a signed `payment.paid` event to `POST /webhook`; the backend verifies the HMAC signature and marks the order paid. The production way. |
| 3 | Polling | The backend asks Kasera `GET /v1/transactions/:id` ("paid yet?" — the API answers `succeeded`) whenever the order page checks in. No public URL needed — great for local dev. |

> **Status:** Kasera Pay is pre-launch — DOKU runs in sandbox, so demo
> payments cannot complete with real money yet. Everything here works against
> a local/staging stack today and goes live unchanged at launch.

## Run it locally (3 commands)

You need Go 1.22+ and a Kasera Pay account with an API key
(Dashboard → Developer → API keys).

```sh
cp .env.example .env   # then put your kp_live_... key in KASERA_API_KEY
go run .
open http://localhost:3300
```

`KASERA_API_BASE` defaults to `http://localhost:8888` (the local Kasera Pay
dev gateway; the developer API lives under `<base>/v1/...`). Point it at the
production host to run against the real thing.

### Webhooks in local dev

Kasera only delivers webhooks to **public https** URLs, so `localhost` won't
receive them directly. Use any tunnel:

```sh
ngrok http 3300
```

then set your webhook endpoint in the dashboard (Developer → Webhook) to
`https://<your-tunnel>/webhook`, and copy the `whsec_...` secret it shows you
— once, at creation, never again — into `WEBHOOK_SECRET` in `.env`.

No tunnel handy? The **polling** case (3) demonstrates the same
"order flips to paid" loop with zero public exposure.

## Local demo — against the Kasera Pay dev stack (verified)

With the product's dev stack running (gateway on `:8888`, Postgres on
`:55432`), this whole demo works end-to-end with no tunnel and no real money.
What was verified, exactly:

1. **Credentials via the real dashboard endpoints** (no seeding shortcuts):
   log in as a seed merchant, then `POST /api/v1/developer/keys` returns the
   `kp_live_...` secret once — that goes into `.env` as `KASERA_API_KEY`.
2. **Webhook registration is impossible locally** — confirmed, not assumed:
   `PUT /api/v1/developer/webhook` rejects `http://localhost:3300/webhook`
   with `url_not_https`, and `https://localhost:3300/webhook` with
   `url_private` (the SSRF guard allows public https only, no dev override).
   So `WEBHOOK_SECRET` in `.env` is a value you invent;
   `scripts/simulate-paid.sh` signs with it using the product's exact scheme.
3. **`scripts/simulate-paid.sh <payreq_id>`** — LOCAL DEMOS ONLY — fakes the
   buyer paying: it calls the product's own dev-mode simulator
   (`POST /api/v1/checkout/<token>/simulate-payment`, the same endpoint the
   checkout page's simulate button uses, which runs the real
   pending→succeeded transition including the webhook outbox row), then
   delivers the signed `payment.paid` webhook to this demo itself, since the
   product's dispatcher can't (see 2).

Verified end-to-end, one order each:

| Case | Result |
|------|--------|
| 1 Redirect | ✅ order created, `checkout_url` serves the hosted checkout page (now rendering the `order_items` line we send); after simulate-paid the checkout page shows **paid**. Caveat: "back to the store" (`return_url`) shipped in the product, but it accepts **https only** — no dev exception — and this demo serves plain http, so locally it stays a manual browser-back. See "Redirect-back" below. |
| 2 Webhook | ✅ simulate-paid delivered the signed event; the demo verified the HMAC and flipped the order to **paid** (no polling involved — case≠polling never refreshes from Kasera, so the webhook path alone did it). |
| 3 Polling | ✅ with the webhook send skipped (`WEBHOOK_SECRET= scripts/simulate-paid.sh ...`), `GET /api/orders/{id}` refreshed from `GET /v1/transactions/:id` (Kasera says `succeeded`) and returned **paid**. |
| 0 No code | n/a — lives entirely in the Kasera dashboard, nothing to wire. |

## What the demo sends Kasera (KAS-2203 fields)

Order creation uses the enriched `POST /v1/transactions` body (the API
renamed its routes from `/v1/payment-requests` to `/v1/transactions` —
KAS-2285 merged payins and payouts into one resource — but ids keep their
`payreq_` prefix and the webhook keeps its `payment.paid` name):

```json
{
  "amount": 189000,
  "description": "Kasera Threads — Batik Print Tee",
  "external_id": "a1b2c3d4e5f60718",
  "merchant_ref": "a1b2c3d4e5f60718",
  "customer": { "name": "Demo Buyer" },
  "order_items": [
    { "name": "Batik Print Tee", "price": 189000, "quantity": 1 }
  ]
}
```

- **`merchant_ref`** — our order ID, echoed back in every response (shown on
  the order page so refs line up between our logs and Kasera's dashboard).
  A label only: it is stored, echoed and filterable, and never deduplicates
  anything — only the `Idempotency-Key` header does that.
- **`order_items`** — rendered on the hosted checkout so the buyer sees the
  item line, not just an amount. Kasera rejects a list whose
  Σ price×quantity disagrees with `amount`; `amount` stays authoritative.
- **`customer`** — who the developer says is paying; shows up in the
  dashboard.

And the response now carries them back:

```json
{
  "id": "payreq_9b2f…",
  "status": "pending",
  "amount": 189000,
  "merchant_ref": "a1b2c3d4e5f60718",
  "customer": { "name": "Demo Buyer" },
  "order_items": [
    { "name": "Batik Print Tee", "price": 189000, "quantity": 1 }
  ],
  "checkout_url": "http://localhost:8888/p/…",
  "payment_method": "QRIS",
  "instructions": { "title": "Cara membayar dengan QRIS", "steps": ["…"] }
}
```

### Redirect-back (`return_url`) — docs-only in this demo

The full-fat body would also include:

```json
{
  "return_url": "http://localhost:3300/order.html?order=a1b2c3d4e5f60718"
}
```

After payment the hosted checkout sends the buyer to
`<return_url>?id=payreq_…&status=succeeded`. But Kasera validates `return_url`
as **https only** — a payment page never redirects somewhere unencrypted,
and (verified against the validator) there is **no dev allowance for http
localhost** — so this plain-http demo cannot send it and the field stays out
of `main.go`.

<!-- TODO: send return_url from main.go once this demo runs behind https
     (tunnel or real deploy), or if the product grows a dev allowance for
     http://localhost. order.html already handles the arrival. -->

`order.html` handles the arrival anyway (`?order=…&status=succeeded`) — and
deliberately **never trusts the query param**: anyone can type
`status=succeeded` into an address bar, so the page always confirms via
`GET /api/orders/{id}` before showing PAID.

## Endpoints (all in `main.go`)

```
POST /api/orders        {item_id, case}  -> create order + Kasera transaction
GET  /api/orders/{id}                    -> order status (case=polling refreshes from Kasera)
POST /webhook                            -> Kasera's signed payment.paid callback
```

## Security notes — the parts worth stealing

- **The API key lives server-side only.** `KASERA_API_KEY` can create
  payment requests in your name; it is read from the environment and never
  sent to the browser. If a key appears in frontend JS, it's public.
- **The price never comes from the client.** The browser sends an
  `item_id`; the server looks the amount up in its own table. A client that
  names its own price pays Rp 1 for everything.
- **Webhook signatures are verified, constant-time.** `POST /webhook` is a
  public URL — anyone can POST "your order is paid" to it. The
  `Kasera-Signature` header (hex HMAC-SHA256 over the exact body bytes,
  keyed with `whsec_...`) is recomputed and compared with `hmac.Equal`; a
  plain `==` would leak how much of a guessed signature was right. This is
  the one code path with its own test (`main_test.go`).
- **Idempotency everywhere it matters.** Order creation sends an
  `Idempotency-Key` (optional on Kasera's API, but the best practice: a
  retried request must not charge twice, and without the header every retry
  is its own charge), and the webhook handler is safely re-entrant because
  delivery is at-least-once.
- **Secrets never land in git.** `.env` is gitignored; `.env.example` ships
  the shape with empty values.

## What this demo deliberately skips

Orders live in an in-memory map and vanish on restart; there are no users,
no carts, no HTTPS termination, no queue. Every such shortcut is marked with
a `ponytail:` comment in the source naming the ceiling and the upgrade path.

## License

[MIT](LICENSE)
