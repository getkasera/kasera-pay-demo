# Kasera Threads salesbot — an AI agent that takes payments

A ~100-line CLI chatbot that sells the demo shop's shirts and **gets paid
through Kasera Pay**, using [`kasera-pay-mcp`](https://pay.kasera.id/docs/mcp)
— the same MCP server any agent framework can mount. The agent quotes real
fees, creates a real (sandbox) payment request, hands you the checkout link,
simulates the payment, and confirms it — the full money loop, zero real money.

## Run it

You need Node 20+, an [Anthropic API key](https://console.anthropic.com), and
a Kasera Pay **test** API key (`kp_test_…` — Dashboard → Developer → API keys).

```sh
cd agent
npm install
ANTHROPIC_API_KEY=sk-ant-… KASERA_API_KEY=kp_test_… npm start
```

Then talk to it:

> **you:** mau beli hoodie dong
>
> **bot:** Kasera Threads Hoodie, Rp 429.000 … *(creates the payment request)*
> Bayar di sini: https://pay.kasera.id/p/…

Ask it to "simulate the payment" and it drives the sandbox transaction to
`succeeded`, then confirms via `get_payment`.

## What this demonstrates

- The agent never sees your API key — it lives in the MCP server's
  environment, and tools never accept keys as arguments.
- The model used is **Claude Haiku 4.5** (`claude-haiku-4-5`): a salesbot
  that calls four payment tools doesn't need a frontier model, and this keeps
  the demo cheap to run.
- Safety rails come from the MCP server, not this script: with a test key
  everything works; with a `kp_live_` key, creating payments is refused
  unless the operator deliberately sets `KASERA_ALLOW_LIVE=true`, and
  `simulate_payment` never works on live keys.

## How it works

`chat.js` uses the [Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk)
and mounts the MCP server like any MCP client would:

```
npx kasera-pay-mcp   (stdio, KASERA_API_KEY passed via env)
```

Only the Kasera Pay tools are allowed — the agent has no filesystem or shell
access. The catalog in the system prompt mirrors the demo shop
(`../public/store.js`); prices are quoted by the agent but **enforced by the
merchant backend** in a real integration — never trust an LLM with amounts,
the same rule as never trusting a browser.

Point `KASERA_BASE_URL` at a local dev stack (`http://localhost:8888`) to run
fully offline.
