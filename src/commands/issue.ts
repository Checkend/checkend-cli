import { orNotFound } from "../api.ts";
import { type Command, parseId, type Session } from "../command.ts";
import { CliError, EXIT } from "../errors.ts";
import { ago } from "../output.ts";

type Issue = Record<string, unknown> & { id: number; title: string };

interface IssuePage {
	data: Issue[];
	next_cursor: string | null;
	ingest?: { mode?: string } | null;
}

const CURSOR = /^[A-Za-z0-9_-]{1,200}$/;

const list: Command = {
	path: ["issue", "list"],
	summary: "List issues, the most recent first (default: the linked project)",
	usage: "[--status <status>] [--project <id|slug>] [--limit <n>] [--cursor <cursor>]",
	details: [
		"  --status <status>    unresolved, resolved or ignored",
		"  --project <id|slug>  Default: the project in .checkend.json, else all projects",
		"  --limit <n>          1 to 100 (default 25)",
		"  --cursor <cursor>    The next page, from the previous list",
	],
	options: {
		status: { type: "string" },
		project: { type: "string" },
		limit: { type: "string" },
		cursor: { type: "string" },
	},
	async run(session) {
		const { out } = session;
		const api = session.api();
		const ref = session.projectRef();
		const project = ref ? await session.project(api, ref) : undefined;
		const status = session.string("status");
		const limit = session.string("limit");
		const page = await api.get<IssuePage>("/v1/issues", {
			project: project ? String(project.id) : undefined,
			status,
			limit,
			cursor: session.string("cursor"),
		});
		warnIngest(session, page.ingest);
		out.data(page, (p) => {
			if (p.data.length === 0) return "No issues.";
			const now = session.context.now();
			const table = out.table(
				["ID", "STATUS", "LEVEL", "EVENTS", "LAST SEEN", "TITLE"],
				p.data.map((i) => [i.id, i.status, i.level, i.times_seen, ago(i.last_seen, now), i.title]),
			);
			if (!p.next_cursor || !CURSOR.test(p.next_cursor)) return table;
			const args = [
				status && `--status ${status}`,
				ref && session.string("project") && `--project ${project?.slug}`,
				limit && `--limit ${limit}`,
			].filter(Boolean);
			return `${table}\n\nMore: ce issue list ${[...args, `--cursor ${p.next_cursor}`].join(" ")}`;
		});
		return EXIT.ok;
	},
};

const view: Command = {
	path: ["issue", "view"],
	summary: "Show one issue",
	usage: "<id>",
	details: [
		"  <id>  The issue number (42), or with a project prefix (api-42) to check the project",
	],
	args: ["id"],
	async run(session) {
		const input = session.args[0] ?? "";
		const match = /^(?:(.+)-)?(\d+)$/.exec(input);
		const id = parseId(match?.[2], "The issue ID");
		const prefix = match?.[1];
		const api = session.api();
		const body = await orNotFound(
			api.get<{ data: Issue }>(`/v1/issues/${id}`),
			`Issue ${id} not found.`,
		);
		if (prefix) {
			const project = await session.project(api, prefix);
			if (String(project.id) !== String(body.data.project_id)) {
				throw new CliError(`Issue ${id} is not in project ${project.slug}.`, EXIT.notFound);
			}
		}
		session.out.data(body, ({ data }) =>
			card(session, data, [
				[`ce event view ${data.latest_event_id}`, "the latest event", data.latest_event_id != null],
				["ce issue list", "all issues", true],
			]),
		);
		return EXIT.ok;
	},
};

const next: Command = {
	path: ["next"],
	summary: "Show the issue to work on next (the most recent unresolved issue)",
	usage: "[--project <id|slug>]",
	options: { project: { type: "string" } },
	async run(session) {
		const api = session.api();
		const ref = session.projectRef();
		const project = ref ? await session.project(api, ref) : undefined;
		const page = await api.get<IssuePage>("/v1/issues", {
			project: project ? String(project.id) : undefined,
			status: "unresolved",
			limit: "1",
		});
		warnIngest(session, page.ingest);
		const top = page.data[0];
		if (!top) {
			session.out.data({ data: null }, () => "No unresolved issues.");
			return EXIT.ok;
		}
		const body = await api.get<{ data: Issue }>(`/v1/issues/${top.id}`);
		session.out.data(body, ({ data }) =>
			card(session, data, [
				[`ce issue view ${data.id}`, "the issue", true],
				[`ce event view ${data.latest_event_id}`, "the latest event", data.latest_event_id != null],
			]),
		);
		return EXIT.ok;
	},
};

function card(session: Session, issue: Issue, commands: [string, string, boolean][]): string {
	const { out } = session;
	const now = session.context.now();
	const when = (value: unknown) => (value ? `${ago(value, now)} (${value})` : "-");
	const releases = [issue.first_release, issue.last_release].filter(Boolean);
	const shown = commands.filter(([, , show]) => show);
	const width = Math.max(...shown.map(([command]) => command.length));
	return [
		out.bold(`#${issue.id} ${issue.title}`),
		"",
		out.fields([
			["Status", issue.status],
			["Level", out.level(issue.level)],
			["Culprit", issue.culprit],
			["Events", `${issue.times_seen} (${issue.stored_events} stored)`],
			["First seen", when(issue.first_seen)],
			["Last seen", when(issue.last_seen)],
			["Releases", releases.length ? [...new Set(releases)].join(" … ") : "-"],
			["Project", issue.project_id],
		]),
		"",
		"Next:",
		...shown.map(([command, what]) => `  ${command.padEnd(width)}  ${out.dim(what)}`),
	].join("\n");
}

function warnIngest(session: Session, ingest: { mode?: string } | null | undefined): void {
	if (ingest?.mode === "counters_only") {
		session.out.note(
			"Note: ingest stores counters only (budget mode) until 00:00 UTC. New events are counted, not stored.",
		);
	}
}

export const issueCommands: Command[] = [list, view];
export const nextCommand = next;
