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
| 1 | Redirect only | It doesn't (on purpose). Backend creates a payment request, buyer is redirected to checkout, and… that's it. Shows *why* cases 2 and 3 exist. |
| 2 | Webhook | Kasera POSTs a signed `payment.paid` event to `POST /webhook`; the backend verifies the HMAC signature and marks the order paid. The production way. |
| 3 | Polling | The backend asks Kasera `GET /v1/payment-requests/:id` ("paid yet?") whenever the order page checks in. No public URL needed — great for local dev. |

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

## Endpoints (all in `main.go`)

```
POST /api/orders        {item_id, case}  -> create order + Kasera payment request
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
  `Idempotency-Key` (Kasera requires it — a retried request must not charge
  twice), and the webhook handler is safely re-entrant because delivery is
  at-least-once.
- **Secrets never land in git.** `.env` is gitignored; `.env.example` ships
  the shape with empty values.

## What this demo deliberately skips

Orders live in an in-memory map and vanish on restart; there are no users,
no carts, no HTTPS termination, no queue. Every such shortcut is marked with
a `ponytail:` comment in the source naming the ceiling and the upgrade path.

## License

[MIT](LICENSE)
