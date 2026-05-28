/**
 * bastu-bot as a Cloudflare Worker.
 *
 * Two entry points:
 *   - fetch():     Telegram webhook + GET /temperatures. Replies to /bastu and
 *                  /sauna in the chat the command came from.
 *   - scheduled(): Cron poller. Watches each target's temperature and pushes
 *                  LIT / READY / cooling-down alerts to ALERT_CHAT_ID, firing
 *                  once per state transition (see evaluateTarget).
 */

interface Target {
	name: string;
	url: string;
	sensor?: number;
}

interface Env {
	/** Bot API token. Store as a secret: `wrangler secret put BOT_TOKEN`. */
	BOT_TOKEN: string;
	/** JSON array of Target objects, set as a var in wrangler.jsonc. */
	TARGETS: string;
	/** Optional header line; defaults to "Current BASTU temperature:". */
	MESSAGE_HEADER?: string;
	/**
	 * Optional shared secret. If set, it must match the
	 * X-Telegram-Bot-Api-Secret-Token header (configured via setWebhook),
	 * otherwise the request is rejected. Strongly recommended.
	 */
	WEBHOOK_SECRET?: string;

	/** KV namespace holding per-target alert state. */
	STATE: KVNamespace;
	/** Chat ID for scheduled alerts. Secret: `wrangler secret put ALERT_CHAT_ID`. */
	ALERT_CHAT_ID?: string;
	/** Alert thresholds in °C (strings from env). Defaults: 40 / 70 / 30. */
	LIT_TEMP?: string;
	READY_TEMP?: string;
	RESET_TEMP?: string;
}

// Shape of the sensor endpoint response (matches TempData in bastu.go).
interface TempData {
	sensor_count: number;
	temperatures: { sensor: number; temperature: number }[];
}

// Minimal slice of the Telegram Update object that we actually use.
interface TelegramUpdate {
	message?: {
		chat: { id: number };
		text?: string;
	};
}

// Alert lifecycle for one target. Stored in KV as `state:<name>`.
type Phase = "idle" | "lit" | "ready";
interface AlertState {
	phase: Phase;
	temp: number;
	ts: number;
}

async function fetchTemperature(target: Target): Promise<number> {
	const resp = await fetch(target.url);
	if (!resp.ok) {
		throw new Error(`fetching ${target.url}: HTTP ${resp.status}`);
	}

	const data = (await resp.json()) as TempData;
	const idx = target.sensor ?? 0;

	if (!data.temperatures || idx >= data.temperatures.length) {
		throw new Error(
			`sensor index ${idx} not found (have ${data.temperatures?.length ?? 0} sensors)`,
		);
	}

	return data.temperatures[idx].temperature;
}

async function sendMessage(token: string, chatId: number, text: string): Promise<void> {
	const resp = await fetch(`https://api.telegram.org/bot${token}/sendMessage`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ chat_id: chatId, text }),
	});
	if (!resp.ok) {
		console.error(`sendMessage failed: HTTP ${resp.status} ${await resp.text()}`);
	}
}

/** Extract a lowercase command name from text, stripping any @BotName suffix. */
function parseCommand(text: string): string | null {
	if (!text.startsWith("/")) return null;
	const token = text.split(/\s+/)[0]; // "/bastu@MyBot 12" -> "/bastu@MyBot"
	return token.slice(1).split("@")[0].toLowerCase();
}

/** Parse the TARGETS env var into Target objects, or null if invalid. */
function parseTargets(env: Env): Target[] | null {
	try {
		return JSON.parse(env.TARGETS) as Target[];
	} catch (e) {
		console.error("invalid TARGETS config:", e);
		return null;
	}
}

/** Read a numeric env var, falling back to a default when unset/invalid. */
function numEnv(value: string | undefined, fallback: number): number {
	const n = Number(value);
	return value && Number.isFinite(n) ? n : fallback;
}

/**
 * Advance one target's alert state given the latest reading, sending a Telegram
 * message on each transition. State is only written to KV when the phase
 * changes, so steady-state polling costs zero KV writes.
 *
 * Transitions (with hysteresis between LIT and RESET):
 *   idle  + temp >= LIT    -> lit    🔥 SAUNA IS LIT
 *   lit   + temp >= READY  -> ready  ♨️ SAUNA IS READY
 *   lit/ready + temp <= RESET -> idle ❄️ cooling down
 * The first two are sequential, so a single poll can cascade idle -> ready.
 */
async function evaluateTarget(
	env: Env,
	target: Target,
	temp: number,
	thresholds: { lit: number; ready: number; reset: number },
): Promise<void> {
	const key = `state:${target.name}`;
	const stored = (await env.STATE.get(key, "json")) as AlertState | null;
	let phase: Phase = stored?.phase ?? "idle";
	const before = phase;
	const messages: string[] = [];
	const t = temp.toFixed(1);

	if (phase === "idle" && temp >= thresholds.lit) {
		phase = "lit";
		messages.push(`🔥 ${target.name}: SAUNA IS LIT (${t}°C)`);
	}
	if (phase === "lit" && temp >= thresholds.ready) {
		phase = "ready";
		messages.push(`♨️ ${target.name}: SAUNA IS READY (${t}°C)`);
	}
	if ((phase === "lit" || phase === "ready") && temp <= thresholds.reset) {
		phase = "idle";
		messages.push(`❄️ ${target.name}: cooling down (${t}°C)`);
	}

	if (phase === before) return; // no transition, nothing to persist or send

	await env.STATE.put(key, JSON.stringify({ phase, temp, ts: Date.now() } satisfies AlertState));

	if (!env.ALERT_CHAT_ID) {
		console.error("ALERT_CHAT_ID is not set; skipping alert send");
		return;
	}
	for (const message of messages) {
		await sendMessage(env.BOT_TOKEN, Number(env.ALERT_CHAT_ID), message);
	}
}

export default {
	async fetch(request: Request, env: Env): Promise<Response> {
		const url = new URL(request.url);

		// GET /temperatures: return temperatures as a JSON array.
		if (request.method === "GET" && url.pathname === "/temperatures") {
			const targets = parseTargets(env);
			if (!targets) {
				return new Response("invalid TARGETS config", { status: 500 });
			}

			const results = await Promise.all(
				targets.map(async (target) => {
					try {
						return { name: target.name, temperature: await fetchTemperature(target) };
					} catch (e) {
						console.error(`Error fetching ${target.name}:`, e);
						return {
							name: target.name,
							temperature: null,
							error: e instanceof Error ? e.message : String(e),
						};
					}
				}),
			);

			return new Response(JSON.stringify(results), {
				headers: { "Content-Type": "application/json" },
			});
		}

		if (request.method !== "POST") {
			return new Response("ok", { status: 200 });
		}

		// Verify the request actually came from Telegram.
		if (env.WEBHOOK_SECRET) {
			const got = request.headers.get("X-Telegram-Bot-Api-Secret-Token");
			if (got !== env.WEBHOOK_SECRET) {
				return new Response("forbidden", { status: 403 });
			}
		}

		let update: TelegramUpdate;
		try {
			update = (await request.json()) as TelegramUpdate;
		} catch {
			return new Response("bad request", { status: 400 });
		}

		const msg = update.message;
		if (!msg?.text) {
			return new Response("ok"); // ignore non-text / non-message updates
		}

		const cmd = parseCommand(msg.text);
		if (cmd !== "bastu" && cmd !== "sauna") {
			return new Response("ok"); // not a command we handle
		}

		const targets = parseTargets(env);
		if (!targets) {
			return new Response("ok");
		}

		const header = env.MESSAGE_HEADER || "Current BASTU temperature:";
		const lines = [header];
		for (const target of targets) {
			try {
				const temp = await fetchTemperature(target);
				lines.push(`${target.name}: ${temp.toFixed(2)}°C`);
			} catch (e) {
				console.error(`Error fetching ${target.name}:`, e);
				lines.push(`${target.name}: error (${e instanceof Error ? e.message : e})`);
			}
		}

		await sendMessage(env.BOT_TOKEN, msg.chat.id, lines.join("\n"));
		return new Response("ok");
	},

	async scheduled(_event: ScheduledController, env: Env): Promise<void> {
		const targets = parseTargets(env);
		if (!targets) return;

		const thresholds = {
			lit: numEnv(env.LIT_TEMP, 40),
			ready: numEnv(env.READY_TEMP, 70),
			reset: numEnv(env.RESET_TEMP, 30),
		};

		for (const target of targets) {
			let temp: number;
			try {
				temp = await fetchTemperature(target);
			} catch (e) {
				// Treat an unreachable sensor as "no reading", not a temperature drop.
				console.error(`poll: error fetching ${target.name}:`, e);
				continue;
			}
			await evaluateTarget(env, target, temp, thresholds);
		}
	},
} satisfies ExportedHandler<Env>;
