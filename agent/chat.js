#!/usr/bin/env node
// Kasera Threads salesbot — a CLI chatbot that takes payments through
// Kasera Pay, by mounting the kasera-pay-mcp server like any MCP client.
// Sandbox only: it refuses to start with a live key on purpose.
//
// Run: ANTHROPIC_API_KEY=sk-ant-… KASERA_API_KEY=kp_test_… npm start

import readline from "node:readline/promises";
import { query } from "@anthropic-ai/claude-agent-sdk";

const kaseraKey = process.env.KASERA_API_KEY;
if (!kaseraKey) {
  console.error("Set KASERA_API_KEY to a kp_test_… key (Dashboard → Developer → API keys).");
  process.exit(1);
}
if (kaseraKey.startsWith("kp_live_")) {
  // The MCP server would already refuse live writes without KASERA_ALLOW_LIVE,
  // but this demo has no business near a live key at all.
  console.error("That is a LIVE key. This demo is sandbox-only — use a kp_test_… key.");
  process.exit(1);
}

// The catalog mirrors ../public/store.js. The agent quotes these prices, but
// remember the demo shop's own rule: in a real integration the MERCHANT
// BACKEND owns the price table — never trust an LLM (or a browser) with
// amounts. Kasera Pay charges exactly what the payment request says.
const SYSTEM = `You are the salesbot for Kasera Threads, a small Indonesian
clothing shop that accepts payments through Kasera Pay. Reply in the buyer's
language (Indonesian or English), briefly and warmly.

Catalog (prices in whole rupiah):
- Batik Print Tee — Rp 189.000 (id: tee-batik)
- Heavyweight Plain Tee — Rp 149.000 (id: tee-plain)
- Kasera Threads Hoodie — Rp 429.000 (id: hoodie-kasera)
- Canvas Cap — Rp 119.000 (id: cap-canvas)
- Socks (3 pack) — Rp 89.000 (id: socks-3pack)
- Denim Tote Bag — Rp 159.000 (id: tote-denim)

When the buyer wants to buy:
1. Confirm the item(s) and total.
2. Create the payment request with the Kasera Pay tools (include order_items
   so the checkout shows what they bought; use the item id as the reference).
3. Give them the checkout_url to pay.

This runs in Kasera Pay's sandbox: if the buyer asks to "simulate" or
"pretend to pay", use the simulate tool to drive the payment to succeeded,
then confirm with the payment-lookup tool and thank them. If they ask about
fees, use the pricing tool and answer from its real numbers — never invent
fees. Never ask for card numbers or bank details; Kasera's checkout handles
payment.`;

const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
console.log("Kasera Threads salesbot (sandbox) — ketik pesan Anda, kosongkan untuk keluar.\n");

let inSession = false;
for (;;) {
  const prompt = (await rl.question("you: ")).trim();
  if (!prompt) break;

  for await (const m of query({
    prompt,
    options: {
      model: "claude-haiku-4-5",
      systemPrompt: SYSTEM,
      continue: inSession, // keep one conversation across turns
      mcpServers: {
        "kasera-pay": {
          command: "npx",
          args: ["kasera-pay-mcp"],
          // The key lives here, in the server's env — the model never sees
          // it, and the tools never accept keys as arguments.
          env: {
            KASERA_API_KEY: kaseraKey,
            ...(process.env.KASERA_BASE_URL && { KASERA_BASE_URL: process.env.KASERA_BASE_URL }),
          },
        },
      },
      // Payment tools only — no filesystem, no shell, no web.
      allowedTools: ["mcp__kasera-pay__*"],
      disallowedTools: ["Bash", "Read", "Write", "Edit", "Glob", "Grep", "WebSearch", "WebFetch"],
    },
  })) {
    if (m.type === "assistant") {
      for (const block of m.message.content) {
        if (block.type === "text") console.log(`bot: ${block.text}`);
        else if (block.type === "tool_use")
          console.log(`  ⚙ ${block.name.replace("mcp__kasera-pay__", "")} ${JSON.stringify(block.input)}`);
      }
    } else if (m.type === "result" && m.subtype !== "success") {
      console.error(`error: ${m.subtype}`);
    }
  }
  inSession = true;
}
rl.close();
