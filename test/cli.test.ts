import { existsSync, readdirSync } from "node:fs";
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

describe("help and version", () => {
	it("prints the help with only the commands that exist now", async () => {
		const result = await cli.run(["--help"]);
		expect(result.code).toBe(0);
		for (const command of [
			"login",
			"logout",
			"status",
			"init",
			"next",
			"issue",
			"event",
			"project",
		]) {
			expect(result.stdout).toContain(command);
		}
		for (const later of [
			"brief",
			"claim",
			"note",
			"fix",
			"deploy",
			"release",
			"token",
			"resolve",
		]) {
			const commands = result.stdout.split("Commands:")[1]?.split("Options")[0] ?? "";
			expect(commands).not.toMatch(new RegExp(`^\\s+${later}\\b`, "m"));
		}
	});

	it("prints the help when there is no command", async () => {
		const result = await cli.run([]);
		expect(result.code).toBe(0);
		expect(result.stdout).toContain("Usage: ce <command>");
	});

	it("prints help for a command group", async () => {
		const result = await cli.run(["project", "--help"]);
		expect(result.code).toBe(0);
		expect(result.stdout).toContain("ce project list");
		expect(result.stdout).toContain("ce project create --name <name>");
		expect(api.requests).toEqual([]);
	});

	it("prints help for the issue group", async () => {
		const result = await cli.run(["issue", "--help"]);
		expect(result.code).toBe(0);
		expect(result.stdout).toContain("ce issue list");
		expect(result.stdout).toContain("ce issue view <id>");
	});

	it("prints the version", async () => {
		const result = await cli.run(["--version"]);
		expect(result.code).toBe(0);
		expect(result.stdout.trim()).toMatch(/^\d+\.\d+\.\d+$/);
	});
});

describe("usage errors: a typo never does anything", () => {
	const cases = [
		["cd"],
		["isue", "list"],
		["project", "frob"],
		["project"],
		["project", "list", "--bogus"],
		["project", "list", "extra"],
		["project", "dsn", "a", "b"],
		["init", "--project"],
		["issue", "frob"],
		["issue"],
		["issue", "list", "--bogus"],
		["issue", "list", "extra"],
		["issue", "view"],
		["issue", "view", "abc"],
		["issue", "view", "1", "2"],
		["event", "view", "1.5"],
		["next", "1"],
		["status", "extra"],
		["project", "create"],
		["login", "--url"],
		["--json"],
		["brief", "1"],
		["token", "list"],
	];

	for (const argv of cases) {
		it(`ce ${argv.join(" ")} exits 2, makes no request and writes no file`, async () => {
			const result = await cli.run(argv, { env });
			expect(result.code).toBe(2);
			expect(result.stderr).not.toBe("");
			expect(api.requests).toEqual([]);
			expect(readdirSync(cli.home)).toEqual([]);
			expect(existsSync(join(cli.repo, ".checkend.json"))).toBe(false);
		});
	}

	it("writes a usage error as JSON on stderr when stdout is not a terminal", async () => {
		const result = await cli.run(["cd"]);
		expect(JSON.parse(result.stderr)).toEqual({
			error: 'Unknown command "cd".',
			hint: "Run `ce --help` to see the commands.",
		});
		expect(result.stdout).toBe("");
	});

	it("writes a usage error as text on a terminal", async () => {
		const result = await cli.run(["cd"], { tty: true });
		expect(result.stderr).toContain('Unknown command "cd".');
		expect(result.stderr).toContain("ce --help");
	});
});
