import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { FakeApi, type Json, type Project } from "./support/fake-api.ts";
import { Harness } from "./support/harness.ts";

let api: FakeApi;
let cli: Harness;
let env: Record<string, string>;
let app: Project;
let web: Project;

beforeEach(async () => {
	api = await new FakeApi().start();
	cli = new Harness();
	env = { CHECKEND_URL: api.url, CHECKEND_TOKEN: api.token };
	app = api.addProject("App");
	web = api.addProject("Web");
});
afterEach(() => api.stop());

const normalized = (overrides: Json = {}): Json => ({
	version: 1,
	event_id: "abc",
	timestamp: "2026-10-10T11:00:00.000Z",
	level: "error",
	platform: "node",
	release: "1.2.3",
	environment: "production",
	server_name: "web-1",
	message: null,
	message_template: null,
	exceptions: [
		{
			type: "TypeError",
			value: "Cannot read properties of undefined (reading 'id')",
			module: null,
			mechanism: { type: "onerror", handled: false, synthetic: false },
			frames: [
				{
					filename: "node:internal/process",
					abs_path: null,
					module: null,
					function: "processTicks",
					lineno: 95,
					colno: 5,
					in_app: false,
					context_line: null,
					pre_context: [],
					post_context: [],
				},
				{
					filename: "src/handler.ts",
					abs_path: "/app/src/handler.ts",
					module: null,
					function: "loadUser",
					lineno: 42,
					colno: 17,
					in_app: true,
					context_line: "  return user.id;",
					pre_context: [],
					post_context: [],
				},
			],
		},
	],
	tags: [
		["browser", "Chrome"],
		["route", "/users/:id"],
	],
	user: null,
	request: {
		url: "https://app.example.com/users/7",
		method: "GET",
		query_string: "tab=1",
		headers: [["User-Agent", "Mozilla/5.0"]],
	},
	contexts: null,
	extra: null,
	breadcrumbs: [],
	sdk: { name: "sentry.javascript.node", version: "11.6.0" },
	fingerprint: null,
	truncated: false,
	...overrides,
});

describe("ce issue list", () => {
	beforeEach(() => {
		api.addIssue({
			project_id: app.id,
			title: "TypeError: old",
			last_seen: "2026-10-10T08:00:00.000Z",
		});
		api.addIssue({
			project_id: app.id,
			title: "Error: newest",
			times_seen: 1234,
			last_seen: "2026-10-10T11:55:00.000Z",
		});
		api.addIssue({ project_id: web.id, title: "Error: web", status: "resolved" });
	});

	it("prints a table on a terminal, newest first", async () => {
		const result = await cli.run(["issue", "list"], { env, tty: true });
		expect(result.code).toBe(0);
		const lines = result.stdout.trim().split("\n");
		expect(lines[0]).toMatch(/ID\s+STATUS\s+LEVEL\s+EVENTS\s+LAST SEEN\s+TITLE/);
		expect(lines[1]).toMatch(/2\s+unresolved\s+error\s+1234\s+5m ago\s+Error: newest/);
		expect(result.stdout).toContain("Error: web");
	});

	it("prints the API JSON unchanged when stdout is not a terminal", async () => {
		const result = await cli.run(["issue", "list"], { env });
		const body = JSON.parse(result.stdout);
		expect(body.data.map((i: Json) => i.id)).toEqual([2, 3, 1]);
		expect(body).toHaveProperty("next_cursor", null);
		expect(body).toHaveProperty("ingest");
	});

	it("filters by status, limit and the linked project", async () => {
		writeFileSync(join(cli.repo, ".checkend.json"), JSON.stringify({ project: "app" }));
		const result = await cli.run(["issue", "list", "--status", "unresolved", "--limit", "1"], {
			env,
		});
		const request = api.requests.at(-1);
		expect(request?.path).toBe("/v1/issues");
		expect(Object.fromEntries(request?.query ?? [])).toEqual({
			project: String(app.id),
			status: "unresolved",
			limit: "1",
		});
		expect(JSON.parse(result.stdout).next_cursor).toBe("1");
	});

	it("uses --project before .checkend.json and accepts a public ID", async () => {
		writeFileSync(join(cli.repo, ".checkend.json"), JSON.stringify({ project: "app" }));
		await cli.run(["issue", "list", "--project", String(web.id)], { env });
		expect(api.requests.at(-1)?.query.get("project")).toBe(String(web.id));
	});

	it("shows how to get the next page", async () => {
		const result = await cli.run(["issue", "list", "--limit", "2"], { env, tty: true });
		expect(result.stdout).toContain("ce issue list --limit 2 --cursor 2");
		const next = await cli.run(["issue", "list", "--limit", "2", "--cursor", "2"], { env });
		expect(JSON.parse(next.stdout).data.map((i: Json) => i.id)).toEqual([1]);
	});

	it("exits 3 for an unknown project", async () => {
		const result = await cli.run(["issue", "list", "--project", "nope"], { env });
		expect(result.code).toBe(3);
	});

	it("exits 2 when the server rejects a filter", async () => {
		api.override = (r) =>
			r.path === "/v1/issues"
				? {
						status: 400,
						body: { error: "invalid_request", details: [{ path: "status", message: "Invalid" }] },
					}
				: undefined;
		const result = await cli.run(["issue", "list", "--status", "open"], { env });
		expect(result.code).toBe(2);
		expect(JSON.parse(result.stderr).error).toContain("status");
	});

	it("warns on a terminal when ingest stores counters only", async () => {
		api.ingest = { mode: "counters_only" };
		const result = await cli.run(["issue", "list"], { env, tty: true });
		expect(result.stderr).toContain("counters only");
	});

	it("says when there are no issues", async () => {
		api.issues = [];
		const result = await cli.run(["issue", "list"], { env, tty: true });
		expect(result.stdout).toContain("No issues.");
	});
});

describe("ce issue view", () => {
	let issue: Json;
	beforeEach(() => {
		issue = api.addIssue({
			project_id: app.id,
			title: "TypeError: Cannot read properties of undefined",
			culprit: "loadUser(src/handler.ts)",
			times_seen: 12,
			stored_events: 3,
			first_release: "1.2.0",
			last_release: "1.2.3",
		});
		api.addEvent({ issue_id: Number(issue.id), event: normalized() });
		api.addEvent({ issue_id: Number(issue.id), event: normalized() });
	});

	it("prints a card with the next commands", async () => {
		const result = await cli.run(["issue", "view", "1"], { env, tty: true });
		expect(result.code).toBe(0);
		expect(result.stdout).toContain("#1 TypeError: Cannot read properties of undefined");
		expect(result.stdout).toMatch(/Status\s+unresolved/);
		expect(result.stdout).toMatch(/Events\s+12 \(3 stored\)/);
		expect(result.stdout).toMatch(/Culprit\s+loadUser\(src\/handler.ts\)/);
		expect(result.stdout).toMatch(/Releases\s+1.2.0 … 1.2.3/);
		expect(result.stdout).toContain("ce event view 2");
	});

	it("prints the API JSON unchanged", async () => {
		const result = await cli.run(["issue", "view", "1"], { env });
		expect(JSON.parse(result.stdout)).toEqual({ data: { ...issue, latest_event_id: 2 } });
	});

	it("accepts a project prefix and checks it", async () => {
		const ok = await cli.run(["issue", "view", "APP-1"], { env });
		expect(ok.code).toBe(0);
		const wrong = await cli.run(["issue", "view", "web-1"], { env });
		expect(wrong.code).toBe(3);
		expect(JSON.parse(wrong.stderr).error).toBe("Issue 1 is not in project web.");
	});

	it("exits 3 for an unknown issue", async () => {
		const result = await cli.run(["issue", "view", "99"], { env });
		expect(result.code).toBe(3);
		expect(JSON.parse(result.stderr).error).toBe("Issue 99 not found.");
	});
});

describe("ce event view", () => {
	beforeEach(() => {
		const issue = api.addIssue({ project_id: app.id });
		api.addEvent({ issue_id: Number(issue.id), release: "1.2.3", event: normalized() });
	});

	it("prints the event with the stack, request and tags", async () => {
		const result = await cli.run(["event", "view", "1"], { env, tty: true });
		expect(result.code).toBe(0);
		const out = result.stdout;
		expect(out).toContain("TypeError: Cannot read properties of undefined (reading 'id')");
		expect(out).toContain("loadUser (src/handler.ts:42:17)");
		expect(out).toContain("return user.id;");
		expect(out).toContain("processTicks (node:internal/process:95:5)");
		expect(out).toContain("GET https://app.example.com/users/7?tab=1");
		expect(out).toMatch(/route\s+\/users\/:id/);
		expect(out).toMatch(/Release\s+1.2.3/);
		expect(out.indexOf("loadUser")).toBeLessThan(out.indexOf("processTicks"));
	});

	it("prints one section with --section", async () => {
		const tags = await cli.run(["event", "view", "1", "--section", "tags"], { env, tty: true });
		expect(tags.stdout).toMatch(/browser\s+Chrome/);
		expect(tags.stdout).not.toContain("loadUser");
		const request = await cli.run(["event", "view", "1", "--section", "request"], {
			env,
			tty: true,
		});
		expect(request.stdout).toContain("User-Agent: Mozilla/5.0");
		expect(request.stdout).not.toContain("loadUser");
	});

	it("exits 2 for an unknown section before any request", async () => {
		const result = await cli.run(["event", "view", "1", "--section", "nope"], { env });
		expect(result.code).toBe(2);
		expect(api.requests).toEqual([]);
	});

	it("prints the API JSON unchanged", async () => {
		const result = await cli.run(["event", "view", "1"], { env });
		expect(JSON.parse(result.stdout)).toEqual({ data: api.events[0] });
	});

	it("exits 3 for an unknown event", async () => {
		const result = await cli.run(["event", "view", "99"], { env });
		expect(result.code).toBe(3);
		expect(JSON.parse(result.stderr).error).toBe("Event 99 not found.");
	});

	it("removes terminal escapes from untrusted event text", async () => {
		const hostile = normalized({
			message: "boom\x1b]52;c;cm0gLXJmIH4=\x07 \x1b[8mhidden",
			exceptions: [
				{
					type: "Error\x1b[2J",
					value: "x\u009b6n",
					module: null,
					mechanism: null,
					frames: [
						{
							filename: "a.js\x1b]8;;https://evil\x1b\\",
							function: "f\x07",
							lineno: 1,
							colno: 1,
							in_app: true,
							context_line: "\x1b[1Aevil()",
							abs_path: null,
							module: null,
							pre_context: [],
							post_context: [],
						},
					],
				},
			],
			tags: [["k\x1b[31m", "v\r\x1b[K"]],
			request: { url: "https://x\x1b[0m", method: "GET", query_string: null, headers: [] },
		});
		api.events[0] = { ...api.events[0], event: hostile };
		const result = await cli.run(["event", "view", "1"], { env, tty: true });
		expect(result.code).toBe(0);
		expect(result.all).not.toMatch(/[\x00-\x08\x0b-\x1f\x7f-\x9f]/);
		expect(result.stdout).toContain("boom");
		expect(result.stdout).toContain("evil()");
		const json = await cli.run(["event", "view", "1"], { env });
		expect(json.stdout).not.toMatch(/[\x00-\x09\x0b-\x1f\x7f-\x9f]/);
		expect(JSON.parse(json.stdout).data.event).toEqual(hostile);
	});
});

describe("ce next", () => {
	it("shows the most recent unresolved issue as a card with the next commands", async () => {
		api.addIssue({
			project_id: app.id,
			title: "Error: older",
			last_seen: "2026-10-10T09:00:00.000Z",
		});
		const top = api.addIssue({
			project_id: app.id,
			title: "Error: top",
			last_seen: "2026-10-10T11:00:00.000Z",
		});
		api.addIssue({
			project_id: app.id,
			title: "Error: resolved",
			status: "resolved",
			last_seen: "2026-10-10T11:59:00.000Z",
		});
		api.addEvent({ issue_id: Number(top.id) });
		const result = await cli.run(["next"], { env, tty: true });
		expect(result.code).toBe(0);
		expect(result.stdout).toContain("#2 Error: top");
		expect(result.stdout).toContain("ce issue view 2");
		expect(result.stdout).toContain("ce event view 1");
		const list = api.requests.find((r) => r.path === "/v1/issues");
		expect(Object.fromEntries(list?.query ?? [])).toEqual({ status: "unresolved", limit: "1" });
	});

	it("prints the issue as JSON", async () => {
		const issue = api.addIssue({ project_id: app.id });
		const result = await cli.run(["next"], { env });
		expect(JSON.parse(result.stdout)).toEqual({ data: { ...issue, latest_event_id: null } });
	});

	it("uses the linked project", async () => {
		api.addIssue({ project_id: app.id });
		writeFileSync(join(cli.repo, ".checkend.json"), JSON.stringify({ project: String(web.id) }));
		const result = await cli.run(["next"], { env });
		expect(api.requests.find((r) => r.path === "/v1/issues")?.query.get("project")).toBe(
			String(web.id),
		);
		expect(JSON.parse(result.stdout)).toEqual({ data: null });
	});

	it("says when there is nothing to do", async () => {
		const result = await cli.run(["next"], { env, tty: true });
		expect(result.code).toBe(0);
		expect(result.stdout).toContain("No unresolved issues.");
	});
});
