// Runs the real entry point as a child process.
import { spawnSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const bin = join(import.meta.dirname, "..", "src", "bin.ts");
const home = mkdtempSync(join(tmpdir(), "checkend-cli-bin-"));

function ce(args: string[], input = "") {
	return spawnSync(process.execPath, ["--no-warnings", bin, ...args], {
		input,
		encoding: "utf8",
		env: { PATH: process.env.PATH, HOME: home, XDG_CONFIG_HOME: join(home, ".config") },
		cwd: home,
	});
}

describe("the ce binary", () => {
	it("prints the version", () => {
		const result = ce(["--version"]);
		expect(result.status).toBe(0);
		expect(result.stdout.trim()).toMatch(/^\d+\.\d+\.\d+$/);
	});

	it("exits 2 for an unknown command", () => {
		const result = ce(["cd", ".."]);
		expect(result.status).toBe(2);
		expect(JSON.parse(result.stderr).error).toBe('Unknown command "cd".');
		expect(result.stdout).toBe("");
	});

	it("exits 5 when nobody is logged in", () => {
		const result = ce(["issue", "list"]);
		expect(result.status).toBe(5);
		expect(JSON.parse(result.stderr).hint).toBe("Run `ce login`.");
	});

	it("reads the token from stdin for login and reports an unreachable instance", () => {
		const result = ce(["login", "--url", "http://127.0.0.1:9", "--with-token"], "secret-token\n");
		expect(result.status).toBe(1);
		expect(JSON.parse(result.stderr).error).toContain("Cannot reach http://127.0.0.1:9");
		expect(result.stderr).not.toContain("secret-token");
	});
});
