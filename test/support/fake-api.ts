// An in-process fake of the Checkend API (GET /health and /v1). It follows guides/api.md of
// the server. Tests change its state and can replace any response with `override`.
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";

export type Json = Record<string, unknown>;

export interface FakeRequest {
	method: string;
	path: string;
	query: URLSearchParams;
	authorization: string | undefined;
	body: unknown;
}

export interface FakeReply {
	status: number;
	body?: unknown;
	headers?: Record<string, string>;
	raw?: string;
}

export interface Project extends Json {
	id: number;
	name: string;
	slug: string;
	platform: string | null;
	created_at: string;
	dsns: string[];
}

export class FakeApi {
	token = "ce_test_token_0123456789";
	url = "";
	health: Json = {
		status: "ok",
		version: "0.2.0",
		migrations: { level: 3, latest: "0003_tokens.sql", applied: 3 },
		ingest: { mode: "full", write_failures_today: 0, retry_after_seconds: 0 },
		time: "2026-10-10T12:00:00.000Z",
	};
	ingest: Json = { mode: "full", rows_written_today: 10 };
	projects: Project[] = [];
	issues: Json[] = [];
	events: Json[] = [];
	requests: FakeRequest[] = [];
	override: ((request: FakeRequest) => FakeReply | undefined) | undefined;
	private server: Server | undefined;
	private nextProjectId = 4817305926;

	async start(): Promise<this> {
		this.server = createServer((req, res) => void this.handle(req, res));
		await new Promise<void>((resolve) => this.server?.listen(0, "127.0.0.1", resolve));
		const address = this.server.address();
		if (!address || typeof address === "string") throw new Error("no address");
		this.url = `http://127.0.0.1:${address.port}`;
		return this;
	}

	async stop(): Promise<void> {
		await new Promise<void>((resolve) => this.server?.close(() => resolve()));
	}

	addProject(name: string, platform: string | null = "node"): Project {
		const id = this.nextProjectId;
		this.nextProjectId += 1111111;
		const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, "-");
		const host = new URL(this.url).host;
		const project: Project = {
			id,
			name,
			slug,
			platform,
			created_at: "2026-10-10T12:00:00.000Z",
			dsns: [`http://key${id}@${host}/${id}`],
		};
		this.projects.push(project);
		return project;
	}

	addIssue(fields: Json & { project_id: number }): Json {
		const id = this.issues.length + 1;
		const issue = {
			id,
			title: `Error: issue ${id}`,
			culprit: "app.js in handler",
			type: "Error",
			level: "error",
			platform: "node",
			status: "unresolved",
			times_seen: 1,
			stored_events: 1,
			first_seen: "2026-10-10T10:00:00.000Z",
			last_seen: "2026-10-10T11:00:00.000Z",
			first_release: null,
			last_release: null,
			fingerprint: `fp${id}`,
			fingerprint_version: 1,
			...fields,
		};
		this.issues.push(issue);
		return issue;
	}

	addEvent(fields: Json & { issue_id: number; event?: Json }): Json {
		const issue = this.issues.find((i) => i.id === fields.issue_id);
		const id = this.events.length + 1;
		const event = {
			id,
			project_id: issue?.project_id,
			event_id: `e${String(id).padStart(31, "0")}`,
			timestamp: "2026-10-10T11:00:00.000Z",
			received_at: "2026-10-10T11:00:01.000Z",
			level: "error",
			release: null,
			environment: "production",
			...fields,
		};
		this.events.push(event);
		return event;
	}

	private async handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
		const chunks: Buffer[] = [];
		for await (const chunk of req) chunks.push(chunk as Buffer);
		const text = Buffer.concat(chunks).toString("utf8");
		const url = new URL(req.url ?? "/", "http://fake");
		let body: unknown = null;
		try {
			body = text ? JSON.parse(text) : null;
		} catch {
			body = text;
		}
		const request: FakeRequest = {
			method: req.method ?? "GET",
			path: url.pathname,
			query: url.searchParams,
			authorization: req.headers.authorization,
			body,
		};
		this.requests.push(request);
		const reply = this.override?.(request) ?? this.route(request);
		res.writeHead(reply.status, { "content-type": "application/json", ...reply.headers });
		res.end(reply.raw ?? (reply.body === undefined ? "" : JSON.stringify(reply.body)));
	}

	private route(request: FakeRequest): FakeReply {
		const { method, path, query } = request;
		if (method === "GET" && path === "/health") return { status: 200, body: this.health };
		if (!path.startsWith("/v1/")) return { status: 404, body: { error: "not_found" } };
		if (request.authorization !== `Bearer ${this.token}`) {
			return { status: 401, body: { error: "unauthorized" } };
		}

		if (method === "GET" && path === "/v1/projects") {
			return { status: 200, body: { data: this.projects } };
		}
		if (method === "POST" && path === "/v1/projects") {
			const input = request.body as { name?: unknown; platform?: unknown } | null;
			if (typeof input?.name !== "string" || !input.name.trim()) {
				return {
					status: 400,
					body: { error: "invalid_request", details: [{ path: "name", message: "Invalid" }] },
				};
			}
			const platform = typeof input.platform === "string" ? input.platform : null;
			const { dsns, ...project } = this.addProject(input.name, platform);
			return { status: 201, body: { data: { ...project, dsn: dsns[0] } } };
		}
		if (method === "GET" && path === "/v1/issues") {
			let issues = [...this.issues].sort((a, b) =>
				String(b.last_seen).localeCompare(String(a.last_seen)),
			);
			// The project filter takes the public ID or the slug (lowercase).
			const project = query.get("project");
			if (project !== null && !/^[a-z0-9][a-z0-9-]{0,47}$/.test(project)) {
				return {
					status: 400,
					body: { error: "invalid_request", details: [{ path: "project", message: "Invalid" }] },
				};
			}
			if (project) {
				const ids = this.projects
					.filter((p) => String(p.id) === project || p.slug === project)
					.map((p) => p.id);
				if (ids.length === 0) return { status: 404, body: { error: "project_not_found" } };
				issues = issues.filter((i) => ids.includes(i.project_id as number));
			}
			const status = query.get("status");
			if (status) issues = issues.filter((i) => i.status === status);
			const limit = Number(query.get("limit") ?? 25);
			const start = Number(query.get("cursor") ?? 0);
			const page = issues.slice(start, start + limit);
			const next = start + limit < issues.length ? String(start + limit) : null;
			return { status: 200, body: { data: page, next_cursor: next, ingest: this.ingest } };
		}
		const issueMatch = path.match(/^\/v1\/issues\/(\d+)(\/events)?$/);
		if (method === "GET" && issueMatch) {
			const issue = this.issues.find((i) => i.id === Number(issueMatch[1]));
			if (!issue) return { status: 404, body: { error: "not_found" } };
			const events = this.events
				.filter((e) => e.issue_id === issue.id)
				.sort((a, b) => Number(b.id) - Number(a.id));
			if (issueMatch[2]) {
				return { status: 200, body: { data: events.map(({ event: _, ...e }) => e) } };
			}
			return { status: 200, body: { data: { ...issue, latest_event_id: events[0]?.id ?? null } } };
		}
		const eventMatch = path.match(/^\/v1\/events\/(\d+)$/);
		if (method === "GET" && eventMatch) {
			const event = this.events.find((e) => e.id === Number(eventMatch[1]));
			return event
				? { status: 200, body: { data: event } }
				: { status: 404, body: { error: "not_found" } };
		}
		return { status: 404, body: { error: "not_found" } };
	}
}
