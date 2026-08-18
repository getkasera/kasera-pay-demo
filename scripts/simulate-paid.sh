#!/usr/bin/env bash
# =============================================================================
# LOCAL DEMOS ONLY. This script fakes a buyer paying against the LOCAL Kasera
# Pay dev stack. It has no place anywhere near production — in real life the
# payment provider's callback drives this transition.
# =============================================================================
#
# Usage:   scripts/simulate-paid.sh <transaction_id>
#          (payreq_<uuid> as returned by the API, or the bare uuid — the
#          payreq_ prefix survived the /v1/transactions rename on purpose)
#
# How it works:
#
#   1. Looks up the transaction's checkout token in the dev DB (psql — this
#      is the only DB touch, a read).
#   2. Calls the product's own dev-mode simulator,
#      POST /api/v1/checkout/<token>/simulate-payment — the endpoint the
#      checkout page's "simulate" button uses. That runs the REAL transition:
#      pending -> succeeded with the status guard, plus the audit row
#      and the webhook/notification outbox rows, all in one statement.
#   3. Delivers the payment.paid webhook to this demo itself. The product's
#      dispatcher would do this, but it refuses localhost (SSRF guard:
#      https-only, no private addresses), so registering our /webhook with
#      the product is impossible locally. We play postman instead: same
#      payload shape, same signature scheme (hex HMAC-SHA256 of the exact
#      body in the Kasera-Signature header), keyed with WEBHOOK_SECRET from
#      .env. Same bytes on the wire, different courier.
#
# Skip step 3 (e.g. to watch the polling case do its own work):
#   WEBHOOK_SECRET= scripts/simulate-paid.sh payreq_...
set -euo pipefail
cd "$(dirname "$0")/.."

DB_URL="${DB_URL:-postgres://kasera:kasera@localhost:55432/kasera_pay}"
KASERA_API_BASE="${KASERA_API_BASE:-http://localhost:8888}"
DEMO_URL="${DEMO_URL:-http://localhost:3300}"

# Pull WEBHOOK_SECRET from .env unless the caller already set (or blanked) it.
if [ -z "${WEBHOOK_SECRET+x}" ] && [ -f .env ]; then
  WEBHOOK_SECRET="$(sed -n 's/^WEBHOOK_SECRET=//p' .env)"
fi

ID="${1:?usage: scripts/simulate-paid.sh <transaction_id>}"
ID="${ID#payreq_}" # accept payreq_<uuid> or bare <uuid>

# --- 1: transaction id -> checkout token (the simulator is keyed by the
# buyer-facing token, not the developer-facing id) ---------------------------
TOKEN="$(psql "$DB_URL" -qtA -c \
  "SELECT checkout_token FROM transactions WHERE id = '$ID'")"
if [ -z "$TOKEN" ]; then
  echo "no transaction with id $ID in the dev DB" >&2
  exit 1
fi

# --- 2: the product's own dev simulator runs the real paid transition -------
RES="$(curl -sS -X POST "$KASERA_API_BASE/api/v1/checkout/$TOKEN/simulate-payment")"
case "$RES" in
  *'"status":"succeeded"'*) echo "transaction $ID: succeeded (via product dev simulator)" ;;
  *) echo "simulate-payment failed: $RES" >&2; exit 1 ;;
esac

# --- 3: deliver the webhook ourselves --------------------------------------
if [ -z "${WEBHOOK_SECRET:-}" ]; then
  echo "WEBHOOK_SECRET empty — skipping webhook send (polling/redirect still see paid)"
  exit 0
fi

# The payload fields, straight from the row the simulator just updated.
# (Timestamps on the wire are +07:00, per the API contract; the column behind
# paid_at is settled_at since the transactions merge. The data object still
# says payment_request_id — the wire name survived the rename.)
ROW="$(psql "$DB_URL" -qtA -F'|' -c "
  SELECT w.id, p.amount_gross, p.currency, coalesce(p.external_order_id, ''), p.merchant_ref,
         to_char(p.settled_at AT TIME ZONE 'Asia/Jakarta', 'YYYY-MM-DD\"T\"HH24:MI:SS\"+07:00\"')
  FROM transactions p
  JOIN webhook_events w ON w.payment_request_id = p.id
  WHERE p.id = '$ID' AND w.event_type = 'payment.paid'")"
IFS='|' read -r EVENT_ID AMOUNT CURRENCY EXTERNAL_ID MERCHANT_REF PAID_AT <<<"$ROW"

BODY="$(printf '{"id":"evt_%s","type":"payment.paid","created_at":"%s","data":{"payment_request_id":"payreq_%s","external_id":"%s","merchant_ref":"%s","amount":%s,"currency":"%s","paid_at":"%s"}}' \
  "$EVENT_ID" "$PAID_AT" "$ID" "$EXTERNAL_ID" "$MERCHANT_REF" "$AMOUNT" "$CURRENCY" "$PAID_AT")"
SIG="$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -hex | sed 's/^.*= //')"

curl -sS -o /dev/null -w "webhook -> $DEMO_URL/webhook: HTTP %{http_code}\n" \
  -X POST "$DEMO_URL/webhook" \
  -H 'Content-Type: application/json' \
  -H "Kasera-Signature: $SIG" \
  --data-binary "$BODY"
