import { readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { FakeApi } from "./support/fake-api.ts";
import { Harness } from "./support/harness.ts";

let api: FakeApi;
let cli: Harness;

beforeEach(async () => {
	api = await new FakeApi().start();
	cli = new Harness();
});
afterEach(() => api.stop());

const configPath = () => join(cli.home, ".config", "checkend", "config.json");
const readConfig = () => JSON.parse(readFileSync(configPath(), "utf8"));

describe("ce login", () => {
	it("asks for the URL and the token on a terminal, checks them and saves them", async () => {
		const result = await cli.run(["login"], { tty: true, answers: [`${api.url}/`, api.token] });
		expect(result.code).toBe(0);
		expect(cli.prompts.map((p) => p.secret)).toEqual([false, true]);
		expect(result.stdout).toContain(`Logged in to ${api.url}`);
		expect(result.all).not.toContain(api.token);
		expect(api.requests.map((r) => `${r.method} ${r.path}`)).toEqual([
			"GET /health",
			"GET /v1/projects",
		]);
		expect(api.requests[0]?.authorization).toBeUndefined();
		expect(readConfig()).toEqual({ current: api.url, hosts: { [api.url]: { token: api.token } } });
		expect(statSync(configPath()).mode & 0o777).toBe(0o600);
		expect(statSync(join(cli.home, ".config", "checkend")).mode & 0o777).toBe(0o700);
	});

	it("reads the token from stdin with --with-token", async () => {
		const result = await cli.run(["login", "--url", api.url, "--with-token"], {
			stdin: `${api.token}\n`,
		});
		expect(result.code).toBe(0);
		expect(JSON.parse(result.stdout)).toEqual({ url: api.url, version: "0.2.0" });
		expect(readConfig().hosts[api.url].token).toBe(api.token);
	});

	it("uses XDG_CONFIG_HOME when it is set", async () => {
		const xdg = join(cli.home, "xdg");
		await cli.run(["login", "--url", api.url, "--with-token"], {
			stdin: api.token,
			env: { XDG_CONFIG_HOME: xdg },
		});
		expect(JSON.parse(readFileSync(join(xdg, "checkend", "config.json"), "utf8")).current).toBe(
			api.url,
		);
	});

	it("needs a token when it cannot ask for one", async () => {
		const result = await cli.run(["login", "--url", api.url]);
		expect(result.code).toBe(2);
		expect(JSON.parse(result.stderr).hint).toContain("--with-token");
		expect(api.requests).toEqual([]);
	});

	it("refuses a wrong token with exit 5 and saves nothing", async () => {
		const result = await cli.run(["login", "--url", api.url, "--with-token"], { stdin: "wrong" });
		expect(result.code).toBe(5);
		expect(JSON.parse(result.stderr).error).toMatch(/token/i);
		expect(() => readConfig()).toThrow();
	});

	it("refuses plain HTTP to a remote host before it sends anything", async () => {
		const result = await cli.run(["login", "--url", "http://errors.example.com", "--with-token"], {
			stdin: api.token,
		});
		expect(result.code).toBe(2);
		expect(JSON.parse(result.stderr).error).toContain("HTTPS");
	});

	it("reports an instance that it cannot reach", async () => {
		await api.stop();
		const result = await cli.run(["login", "--url", api.url, "--with-token"], { stdin: "x" });
		expect(result.code).toBe(1);
		expect(JSON.parse(result.stderr).error).toContain(`Cannot reach ${api.url}`);
		api = await new FakeApi().start();
	});

	it("refuses a server that is not Checkend", async () => {
		api.override = (r) => (r.path === "/health" ? { status: 200, raw: "<html>" } : undefined);
		const result = await cli.run(["login", "--url", api.url, "--with-token"], { stdin: api.token });
		expect(result.code).toBe(1);
		expect(JSON.parse(result.stderr).error).toMatch(/not a Checkend/);
	});

	it("does not follow redirects, so the token cannot go to another host", async () => {
		const other = await new FakeApi().start();
		api.override = () => ({ status: 302, headers: { location: `${other.url}/v1/projects` } });
		const result = await cli.run(["project", "list"], {
			env: { CHECKEND_URL: api.url, CHECKEND_TOKEN: api.token },
		});
		expect(result.code).toBe(1);
		expect(other.requests).toEqual([]);
		await other.stop();
	});

	it("keeps other instances when it logs in to a second one", async () => {
		const other = await new FakeApi().start();
		await cli.run(["login", "--url", api.url, "--with-token"], { stdin: api.token });
		await cli.run(["login", "--url", other.url, "--with-token"], { stdin: other.token });
		expect(readConfig().current).toBe(other.url);
		expect(Object.keys(readConfig().hosts)).toEqual([api.url, other.url]);
		await other.stop();
	});
});

describe("ce logout", () => {
	it("removes the token of the current instance", async () => {
		await cli.run(["login", "--url", api.url, "--with-token"], { stdin: api.token });
		const result = await cli.run(["logout"], { tty: true });
		expect(result.code).toBe(0);
		expect(result.stdout).toContain(`Logged out of ${api.url}`);
		expect(readConfig()).toEqual({ hosts: {} });
		const status = await cli.run(["status"]);
		expect(status.code).toBe(5);
		expect(JSON.parse(status.stderr).hint).toContain("ce login");
	});

	it("succeeds when nobody is logged in", async () => {
		const result = await cli.run(["logout"], { tty: true });
		expect(result.code).toBe(0);
		expect(result.stdout).toContain("Not logged in");
	});
});

describe("ce status", () => {
	beforeEach(async () => {
		await cli.run(["login", "--url", api.url, "--with-token"], { stdin: api.token });
		api.requests = [];
	});

	it("shows the instance, version, health, ingest mode and linked project", async () => {
		const project = api.addProject("My App");
		writeFileSync(
			join(cli.repo, ".checkend.json"),
			JSON.stringify({ project: String(project.id), slug: project.slug }),
		);
		const result = await cli.run(["status"], { tty: true });
		expect(result.code).toBe(0);
		expect(result.stdout).toContain(api.url);
		expect(result.stdout).toContain("0.2.0");
		expect(result.stdout).toMatch(/Health\s+ok/);
		expect(result.stdout).toMatch(/Ingest\s+full/);
		expect(result.stdout).toContain(`my-app (${project.id})`);
		expect(result.all).not.toContain(api.token);
	});

	it("prints JSON when stdout is not a terminal", async () => {
		const result = await cli.run(["status"]);
		expect(result.code).toBe(0);
		const status = JSON.parse(result.stdout);
		expect(status).toMatchObject({
			url: api.url,
			authenticated: true,
			project: null,
			health: { status: "ok", version: "0.2.0" },
		});
		expect(result.stdout).not.toContain(api.token);
	});

	it("exits 5 when the token is rejected", async () => {
		api.token = "rotated";
		const result = await cli.run(["status"]);
		expect(result.code).toBe(5);
		expect(JSON.parse(result.stdout).authenticated).toBe(false);
	});

	it("exits 1 when the instance is not healthy", async () => {
		api.override = (r) =>
			r.path === "/health" ? { status: 503, body: { status: "error" } } : undefined;
		const result = await cli.run(["status"]);
		expect(result.code).toBe(1);
	});

	it("warns when ingest stores counters only", async () => {
		api.health = { ...api.health, ingest: { mode: "counters_only" } };
		const result = await cli.run(["status"], { tty: true });
		expect(result.stdout).toMatch(/Ingest\s+counters_only/);
	});
});

describe("configuration", () => {
	it("reads the URL and the token from the environment", async () => {
		const result = await cli.run(["status"], {
			env: { CHECKEND_URL: api.url, CHECKEND_TOKEN: api.token },
		});
		expect(result.code).toBe(0);
	});

	it("also accepts CHECKEND_ADMIN_TOKEN", async () => {
		const result = await cli.run(["status"], {
			env: { CHECKEND_URL: api.url, CHECKEND_ADMIN_TOKEN: api.token },
		});
		expect(result.code).toBe(0);
	});

	it("uses flags before the environment", async () => {
		const result = await cli.run(["status", "--token", api.token], {
			env: { CHECKEND_URL: api.url, CHECKEND_TOKEN: "wrong" },
		});
		expect(result.code).toBe(0);
	});

	it("refuses an http:// URL from the environment", async () => {
		const result = await cli.run(["status"], {
			env: { CHECKEND_URL: "http://errors.example.com", CHECKEND_TOKEN: "x" },
		});
		expect(result.code).toBe(2);
	});

	it("never sends a saved token to a different instance", async () => {
		await cli.run(["login", "--url", api.url, "--with-token"], { stdin: api.token });
		const other = await new FakeApi().start();
		const result = await cli.run(["project", "list", "--url", other.url]);
		expect(result.code).toBe(5);
		expect(JSON.parse(result.stderr).hint).toContain(`ce login --url ${other.url}`);
		expect(other.requests.every((r) => r.authorization === undefined)).toBe(true);
		await other.stop();
	});

	it("ignores a token or URL in .checkend.json", async () => {
		writeFileSync(
			join(cli.repo, ".checkend.json"),
			JSON.stringify({ url: api.url, token: api.token }),
		);
		const result = await cli.run(["project", "list"]);
		expect(result.code).toBe(5);
		expect(api.requests).toEqual([]);
	});

	it("finds .checkend.json in a parent directory of the repo", async () => {
		const project = api.addProject("My App");
		writeFileSync(join(cli.repo, ".checkend.json"), JSON.stringify({ project: project.slug }));
		const { mkdirSync } = await import("node:fs");
		mkdirSync(join(cli.repo, "src", "deep"), { recursive: true });
		const result = await cli.run(["status"], {
			cwd: join(cli.repo, "src", "deep"),
			env: { CHECKEND_URL: api.url, CHECKEND_TOKEN: api.token },
		});
		expect(JSON.parse(result.stdout).project).toMatchObject({ id: project.id, slug: "my-app" });
	});

	it("reports a broken .checkend.json", async () => {
		writeFileSync(join(cli.repo, ".checkend.json"), "{not json");
		const result = await cli.run(["status"], {
			env: { CHECKEND_URL: api.url, CHECKEND_TOKEN: api.token },
		});
		expect(result.code).toBe(1);
		expect(JSON.parse(result.stderr).error).toContain(".checkend.json");
	});
});

describe("the token never appears in output", () => {
	it("hides the token when the server echoes it in an error", async () => {
		api.override = (r) =>
			r.path === "/v1/projects"
				? { status: 500, body: { error: `bad token ${r.authorization}` } }
				: undefined;
		for (const tty of [true, false]) {
			const result = await cli.run(["project", "list"], {
				tty,
				env: { CHECKEND_URL: api.url, CHECKEND_TOKEN: api.token },
			});
			expect(result.code).toBe(1);
			expect(result.all).not.toContain(api.token);
		}
	});
});
