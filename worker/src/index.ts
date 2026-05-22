/**
 * bastu-bot as a Cloudflare Worker.
 *
 * Telegram delivers updates to this Worker via a webhook (set once with
 * setWebhook). On /bastu or /sauna the Worker fetches each target's
 * temperature endpoint and replies in the same chat via sendMessage.
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

export default {
	async fetch(request: Request, env: Env): Promise<Response> {
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

		let targets: Target[];
		try {
			targets = JSON.parse(env.TARGETS) as Target[];
		} catch (e) {
			console.error("invalid TARGETS config:", e);
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
} satisfies ExportedHandler<Env>;
