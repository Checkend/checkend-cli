import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { FakeApi } from "./support/fake-api.ts";
import { Harness } from "./support/harness.ts";

let api: FakeApi;
let cli: Harness;
let env: Record<string, string>;

beforeEach(async () => {
	api = await new FakeApi().start();
	cli = new Harness();
	env = { CHECKEND_URL: api.url, CHECKEND_TOKEN: api.token };
});
afterEach(() => api.stop());

const link = () => JSON.parse(readFileSync(join(cli.repo, ".checkend.json"), "utf8"));

describe("ce project list", () => {
	it("prints a table on a terminal", async () => {
		const a = api.addProject("API Server");
		api.addProject("Web", "javascript");
		const result = await cli.run(["project", "list"], { env, tty: true });
		expect(result.code).toBe(0);
		expect(result.stdout).toMatch(/ID\s+SLUG\s+NAME\s+PLATFORM/);
		expect(result.stdout).toMatch(new RegExp(`${a.id}\\s+api-server\\s+API Server\\s+node`));
		expect(result.stdout).toContain("web");
	});

	it("prints the API JSON unchanged when stdout is not a terminal", async () => {
		api.addProject("API Server");
		const result = await cli.run(["project", "list"], { env });
		expect(JSON.parse(result.stdout)).toEqual({ data: api.projects });
	});

	it("says how to create the first project", async () => {
		const result = await cli.run(["project", "list"], { env, tty: true });
		expect(result.stdout).toContain("ce project create --name");
	});

	it("removes escape sequences from server text on a terminal and escapes them in JSON", async () => {
		api.addProject("Evil\x1b]0;owned\x07\x1b[2J name\u009b");
		const tty = await cli.run(["project", "list"], { env, tty: true });
		expect(tty.stdout).toContain("Evil name");
		expect(tty.stdout).not.toMatch(/[\x1b\x07\u009b]/);
		const json = await cli.run(["project", "list"], { env });
		expect(json.stdout).not.toMatch(/[\x1b\x07\u009b]/);
		expect(JSON.parse(json.stdout).data[0].name).toBe("Evil\x1b]0;owned\x07\x1b[2J name\u009b");
	});
});

describe("ce project create", () => {
	it("creates a project and prints its DSN", async () => {
		const result = await cli.run(["project", "create", "--name", "My App", "--platform", "node"], {
			env,
			tty: true,
		});
		expect(result.code).toBe(0);
		expect(api.requests.at(-1)).toMatchObject({
			method: "POST",
			path: "/v1/projects",
			body: { name: "My App", platform: "node" },
		});
		const project = api.projects[0];
		expect(result.stdout).toContain(`Created project my-app (${project?.id})`);
		expect(result.stdout).toContain(project?.dsns[0]);
	});

	it("prints the API JSON when stdout is not a terminal", async () => {
		const result = await cli.run(["project", "create", "--name", "My App"], { env });
		expect(JSON.parse(result.stdout).data).toMatchObject({
			slug: "my-app",
			dsn: expect.any(String),
		});
		expect(api.requests.at(-1)?.body).toEqual({ name: "My App" });
	});

	it("exits 2 when the server rejects the input", async () => {
		const result = await cli.run(["project", "create", "--name", " "], { env });
		expect(result.code).toBe(2);
		expect(JSON.parse(result.stderr).error).toContain("name");
	});
});

describe("ce project dsn", () => {
	it("prints the DSN of a project by slug or by public ID", async () => {
		const project = api.addProject("My App");
		for (const ref of ["my-app", "MY-APP", String(project.id)]) {
			const result = await cli.run(["project", "dsn", ref], { env, tty: true });
			expect(result.code).toBe(0);
			expect(result.stdout.trim()).toBe(project.dsns[0]);
		}
	});

	it("uses --project, then .checkend.json", async () => {
		const a = api.addProject("A");
		const b = api.addProject("B");
		writeFileSync(join(cli.repo, ".checkend.json"), JSON.stringify({ project: String(a.id) }));
		const linked = await cli.run(["project", "dsn"], { env, tty: true });
		expect(linked.stdout.trim()).toBe(a.dsns[0]);
		const flag = await cli.run(["project", "dsn", "--project", "b"], { env, tty: true });
		expect(flag.stdout.trim()).toBe(b.dsns[0]);
	});

	it("prints JSON with the DSNs", async () => {
		const project = api.addProject("My App");
		const result = await cli.run(["project", "dsn", "my-app"], { env });
		expect(JSON.parse(result.stdout)).toEqual({
			data: { id: project.id, slug: "my-app", dsns: project.dsns },
		});
	});

	it("exits 3 for an unknown project and 2 without a project", async () => {
		const unknown = await cli.run(["project", "dsn", "nope"], { env });
		expect(unknown.code).toBe(3);
		expect(JSON.parse(unknown.stderr).hint).toContain("ce project list");
		const none = await cli.run(["project", "dsn"], { env });
		expect(none.code).toBe(2);
		expect(JSON.parse(none.stderr).hint).toContain("ce init");
	});
});

describe("ce init", () => {
	it("creates a project, writes .checkend.json and prints the DSN and the snippet", async () => {
		writeFileSync(
			join(cli.repo, "package.json"),
			JSON.stringify({ dependencies: { express: "5" } }),
		);
		const result = await cli.run(["init", "--name", "My App"], { env, tty: true });
		expect(result.code).toBe(0);
		const project = api.projects[0];
		expect(api.requests.at(-1)?.body).toEqual({ name: "My App", platform: "node" });
		expect(link()).toEqual({ project: String(project?.id), slug: "my-app" });
		expect(result.stdout).toContain(project?.dsns[0]);
		expect(result.stdout).toContain('import * as Sentry from "@sentry/node"');
		expect(result.stdout).toContain(`Sentry.init({ dsn: "${project?.dsns[0]}" })`);
		expect(result.stdout).toContain(".checkend.json");
	});

	it("never writes the token or the URL to .checkend.json", async () => {
		await cli.run(["init", "--name", "My App"], { env });
		const text = readFileSync(join(cli.repo, ".checkend.json"), "utf8");
		expect(text).not.toContain(api.token);
		expect(Object.keys(JSON.parse(text)).sort()).toEqual(["project", "slug"]);
	});

	it("links an existing project with --project", async () => {
		const project = api.addProject("Existing");
		const result = await cli.run(["init", "--project", String(project.id)], { env });
		expect(result.code).toBe(0);
		expect(api.requests.some((r) => r.method === "POST")).toBe(false);
		expect(link()).toEqual({ project: String(project.id), slug: "existing" });
		expect(JSON.parse(result.stdout)).toMatchObject({
			project: { id: project.id, slug: "existing" },
			dsn: project.dsns[0],
			created: false,
		});
	});

	it("writes .checkend.json at the repo root from a subdirectory", async () => {
		mkdirSync(join(cli.repo, "packages", "web"), { recursive: true });
		await cli.run(["init", "--name", "Web"], { env, cwd: join(cli.repo, "packages", "web") });
		expect(link().slug).toBe("web");
		expect(existsSync(join(cli.repo, "packages", "web", ".checkend.json"))).toBe(false);
	});

	it("asks on a terminal: Enter creates a project named after the repo", async () => {
		api.addProject("Other");
		const result = await cli.run(["init"], { env, tty: true, answers: [""] });
		expect(result.code).toBe(0);
		expect(cli.prompts[0]?.question).toContain("Choose");
		expect(result.stderr).toContain("1) other");
		expect(result.stdout).not.toContain("1) other");
		expect(api.requests.at(-1)?.body).toMatchObject({ name: "repo" });
		expect(link().slug).toBe("repo");
	});

	it("asks on a terminal: a number links that project", async () => {
		api.addProject("Other");
		const second = api.addProject("Second");
		await cli.run(["init"], { env, tty: true, answers: ["2"] });
		expect(api.requests.some((r) => r.method === "POST")).toBe(false);
		expect(link().project).toBe(String(second.id));
	});

	it("exits 2 for a wrong choice and writes nothing", async () => {
		api.addProject("Other");
		const result = await cli.run(["init"], { env, tty: true, answers: ["7"] });
		expect(result.code).toBe(2);
		expect(existsSync(join(cli.repo, ".checkend.json"))).toBe(false);
	});

	it("needs --name or --project when it cannot ask", async () => {
		const result = await cli.run(["init"], { env });
		expect(result.code).toBe(2);
		expect(JSON.parse(result.stderr).hint).toMatch(/--name.*--project|--project.*--name/);
		expect(api.requests.some((r) => r.method === "POST")).toBe(false);
		expect(existsSync(join(cli.repo, ".checkend.json"))).toBe(false);
	});

	it("uses the linked project when .checkend.json exists", async () => {
		const project = api.addProject("Linked");
		writeFileSync(join(cli.repo, ".checkend.json"), JSON.stringify({ project: "linked" }));
		const result = await cli.run(["init"], { env, tty: true });
		expect(result.code).toBe(0);
		expect(cli.prompts).toEqual([]);
		expect(result.stdout).toContain(project.dsns[0]);
		expect(api.requests.some((r) => r.method === "POST")).toBe(false);
	});

	it("detects the platform from the repo", async () => {
		const cases: [string, string, string, string][] = [
			[
				"package.json",
				JSON.stringify({ dependencies: { react: "19" } }),
				"javascript",
				"@sentry/browser",
			],
			["Gemfile", 'gem "rails"', "ruby", "Sentry.init do |config|"],
			["pyproject.toml", "[project]", "python", "sentry_sdk.init("],
			["go.mod", "module x", "go", "sentry.Init("],
			["composer.json", "{}", "php", "\\Sentry\\init("],
		];
		for (const [file, content, platform, snippet] of cases) {
			const repo = new Harness();
			writeFileSync(join(repo.repo, file), content);
			const result = await repo.run(["init", "--name", `App ${platform}`], { env });
			expect(JSON.parse(result.stdout).platform, file).toBe(platform);
			expect(JSON.parse(result.stdout).snippet, file).toContain(snippet);
		}
	});

	it("uses --platform before detection", async () => {
		writeFileSync(join(cli.repo, "Gemfile"), "");
		const result = await cli.run(["init", "--name", "X", "--platform", "python"], { env });
		expect(JSON.parse(result.stdout).platform).toBe("python");
	});

	it("refuses a DSN that could inject code into the snippet", async () => {
		api.override = (r) =>
			r.method === "POST"
				? {
						status: 201,
						body: {
							data: {
								id: 1,
								slug: "x",
								name: "x",
								platform: "node",
								dsn: 'https://k@h/1" }); evil(); ("',
							},
						},
					}
				: undefined;
		const result = await cli.run(["init", "--name", "X"], { env, tty: true });
		expect(result.code).toBe(1);
		expect(result.stdout).not.toContain("evil");
		expect(existsSync(join(cli.repo, ".checkend.json"))).toBe(false);
	});
});
