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
| `ALERT_CHAT_ID`  | secret              | `wrangler secret put ALERT_CHAT_ID` — chat for scheduled alerts |
| `TARGETS`        | var in wrangler.jsonc | JSON array of `{ "name", "url", "sensor"? }`               |
| `MESSAGE_HEADER` | var in wrangler.jsonc | Defaults to `Current BASTU temperature:`                   |
| `LIT_TEMP` / `READY_TEMP` / `RESET_TEMP` | var in wrangler.jsonc | Alert thresholds in °C. Default 40 / 70 / 30. |

The `channel` and `logfile` settings from `config.json` are not needed: the bot
replies to whatever chat sends the command, and logs go to `wrangler tail`.

## Develop & deploy

The cron alerts need a KV namespace. Create it once and paste the printed id
into the `kv_namespaces` block in `wrangler.jsonc`:

```sh
npx wrangler kv namespace create STATE
```

Then:

```sh
npm install
npm run typecheck      # tsc --noEmit
npm run dev            # local dev server
npm run deploy         # publish to Cloudflare
```

## Register the webhook with Telegram

After deploying, point Telegram at the Worker URL once (use the same secret you
set above, if any). The URL can be the `workers.dev` subdomain or a custom
domain bound to the Worker. `--data-urlencode` keeps special characters in the
secret from being mangled.

```sh
curl "https://api.telegram.org/bot<BOT_TOKEN>/setWebhook" \
    --data-urlencode "url=https://bastu-bot.<your-subdomain>.workers.dev/<BOT_TOKEN>" \
    --data-urlencode "secret_token=<WEBHOOK_SECRET>"
```

> Only one consumer can read updates for a given bot token. If the Go daemon
> (or any other poller) is still running anywhere, it will silently drain the
> queue before Telegram can deliver to the Worker. Stop it before registering
> the webhook.

Then `/bastu` or `/sauna` in any chat the bot can see will return the readings.
```sh
npm run tail           # stream live logs
```

## Temperature alerts (cron)

A Cron Trigger polls every target every 2 minutes and pushes status changes to
`ALERT_CHAT_ID`, firing once per transition (hysteresis prevents repeats):

| Transition (per target)        | Message               |
| ------------------------------ | --------------------- |
| temp rises past `LIT_TEMP`     | 🔥 `<name>: SAUNA IS LIT`   |
| temp rises past `READY_TEMP`   | ♨️ `<name>: SAUNA IS READY` |
| temp falls below `RESET_TEMP`  | ❄️ `<name>: cooling down`   |

State is kept per target in the `STATE` KV namespace (`state:<name>`) and only
written on a transition. Find `ALERT_CHAT_ID` by sending `/bastu` and reading
the `chat.id` off `npm run tail` (group IDs are negative). Tune the poll
interval via `triggers.crons` in `wrangler.jsonc`.
