import { orNotFound } from "../api.ts";
import { type Command, parseId } from "../command.ts";
import { EXIT, usageError } from "../errors.ts";
import { ago, type Output } from "../output.ts";

const SECTIONS = ["stack", "request", "tags"] as const;
type Section = (typeof SECTIONS)[number];

interface Frame {
	filename?: string | null;
	abs_path?: string | null;
	function?: string | null;
	lineno?: number | null;
	colno?: number | null;
	in_app?: boolean | null;
	context_line?: string | null;
}

interface NormalizedEvent {
	message?: string | null;
	server_name?: string | null;
	exceptions?: { type?: string | null; value?: string | null; frames?: Frame[] }[];
	tags?: [string, string][];
	request?: {
		url?: string | null;
		method?: string | null;
		query_string?: string | null;
		headers?: [string, string][];
	} | null;
}

type Event = Record<string, unknown> & { id: number; event?: NormalizedEvent | null };

const view: Command = {
	path: ["event", "view"],
	summary: "Show one event: stack, request and tags",
	usage: "<id> [--section stack|request|tags]",
	details: [
		"  <id>                 The Checkend event ID (from `ce issue view`)",
		"  --section <name>     Show only stack, request or tags",
	],
	options: { section: { type: "string" } },
	args: ["id"],
	async run(session) {
		const id = parseId(session.args[0], "The event ID");
		const section = session.string("section");
		if (section !== undefined && !SECTIONS.includes(section as Section)) {
			throw usageError(`Unknown section "${section}".`, `Use one of: ${SECTIONS.join(", ")}.`);
		}
		const body = await orNotFound(
			session.api().get<{ data: Event }>(`/v1/events/${id}`),
			`Event ${id} not found.`,
		);
		session.out.data(body, ({ data }) => {
			const parts = section
				? [render(session.out, data, section as Section)]
				: [
						header(session.out, data, session.context.now()),
						...SECTIONS.map((s) => render(session.out, data, s)),
					];
			return parts.filter(Boolean).join("\n\n");
		});
		return EXIT.ok;
	},
};

function header(out: Output, data: Event, now: Date): string {
	const event = data.event ?? {};
	return [
		out.bold(`Event ${data.id} (${data.event_id})`),
		out.fields([
			["Time", data.timestamp ? `${ago(data.timestamp, now)} (${data.timestamp})` : "-"],
			["Level", out.level(data.level)],
			["Release", data.release],
			["Environment", data.environment],
			["Server", event.server_name],
			["Issue", `#${data.issue_id}`],
			["Message", event.message ?? undefined],
		]),
	].join("\n");
}

function render(out: Output, data: Event, section: Section): string {
	const event = data.event ?? {};
	switch (section) {
		case "stack": {
			const exceptions = [...(event.exceptions ?? [])].reverse();
			if (exceptions.length === 0) return `${out.bold("Stack")}\n  (no stack trace)`;
			const lines = [out.bold("Stack (most recent call first)")];
			for (const exception of exceptions) {
				lines.push(out.red(`${exception.type ?? "Error"}: ${exception.value ?? ""}`));
				for (const frame of [...(exception.frames ?? [])].reverse()) {
					const file = frame.filename ?? frame.abs_path ?? "?";
					const position = [frame.lineno, frame.colno].filter((n) => n != null).join(":");
					const where = position ? `${file}:${position}` : file;
					const text = `  at ${frame.function ?? "?"} (${where})`;
					lines.push(frame.in_app ? text : out.dim(text));
					if (frame.in_app && frame.context_line) lines.push(`       ${frame.context_line.trim()}`);
				}
			}
			return lines.join("\n");
		}
		case "request": {
			const request = event.request;
			if (!request?.url && !request?.method) return `${out.bold("Request")}\n  (no request)`;
			const query = request.query_string ? `?${request.query_string}` : "";
			const headers = (request.headers ?? []).map(([name, value]) => `  ${name}: ${value}`);
			return [
				out.bold("Request"),
				`${request.method ?? ""} ${request.url ?? ""}${query}`.trim(),
				...headers,
			].join("\n");
		}
		case "tags": {
			const tags = event.tags ?? [];
			if (tags.length === 0) return `${out.bold("Tags")}\n  (no tags)`;
			const width = Math.max(...tags.map(([key]) => key.length));
			return [
				out.bold("Tags"),
				...tags.map(([key, value]) => `  ${key.padEnd(width)}  ${value}`),
			].join("\n");
		}
	}
}

export const eventCommands: Command[] = [view];
