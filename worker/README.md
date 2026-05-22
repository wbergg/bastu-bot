# bastu-bot (Cloudflare Worker)

A Cloudflare Worker port of bastu-bot. Unlike the Go daemon (which long-polls
Telegram), this version runs as a **webhook**: Telegram POSTs each update to the
Worker, which fetches the sensor temperatures and replies in the same chat.

> ⚠️ Workers run on Cloudflare's edge and **cannot reach LAN addresses**
> (`192.168.x.x`, etc.). The URLs in `TARGETS` must be publicly reachable —
> e.g. exposed via a Cloudflare Tunnel.

## Configuration

`wrangler.jsonc` is git-ignored (it holds your account-specific `TARGETS`).
Copy the template and edit it before deploying:

```sh
cp wrangler.jsonc.example wrangler.jsonc
```

| Key              | Where               | Notes                                                        |
| ---------------- | ------------------- | ------------------------------------------------------------ |
| `BOT_TOKEN`      | secret              | `wrangler secret put BOT_TOKEN`                              |
| `WEBHOOK_SECRET` | secret (optional)   | `wrangler secret put WEBHOOK_SECRET` — verifies requests     |
| `TARGETS`        | var in wrangler.jsonc | JSON array of `{ "name", "url", "sensor"? }`               |
| `MESSAGE_HEADER` | var in wrangler.jsonc | Defaults to `Current BASTU temperature:`                   |

The `channel` and `logfile` settings from `config.json` are not needed: the bot
replies to whatever chat sends the command, and logs go to `wrangler tail`.

## Develop & deploy

```sh
npm install
npm run typecheck      # tsc --noEmit
npm run dev            # local dev server
npm run deploy         # publish to Cloudflare
```

## Register the webhook with Telegram

After deploying, point Telegram at the Worker URL once (use the same secret you
set above, if any):

```sh
curl "https://api.telegram.org/bot<BOT_TOKEN>/setWebhook" \
  -d "url=https://bastu-bot.<your-subdomain>.workers.dev" \
  -d "secret_token=<WEBHOOK_SECRET>"
```

Then `/bastu` or `/sauna` in any chat the bot can see will return the readings.
```sh
npm run tail           # stream live logs
```
