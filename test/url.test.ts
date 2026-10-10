import { describe, expect, it } from "vitest";
import { CliError } from "../src/errors.ts";
import { normalizeUrl } from "../src/url.ts";

describe("normalizeUrl", () => {
	it("accepts https and removes a trailing slash, query and hash", () => {
		expect(normalizeUrl("https://errors.example.com/")).toBe("https://errors.example.com");
		expect(normalizeUrl("https://errors.example.com/x/?a=1#h")).toBe(
			"https://errors.example.com/x",
		);
	});

	it("accepts http only for localhost and 127.0.0.1", () => {
		expect(normalizeUrl("http://localhost:8787")).toBe("http://localhost:8787");
		expect(normalizeUrl("http://127.0.0.1:8787/")).toBe("http://127.0.0.1:8787");
		for (const url of [
			"http://errors.example.com",
			"http://localhost.example.com",
			"http://127.0.0.2",
			"http://10.0.0.1",
			"http://[::1]",
		]) {
			expect(() => normalizeUrl(url), url).toThrow(/HTTPS/);
		}
	});

	it("refuses other schemes, credentials in the URL and bad URLs", () => {
		expect(() => normalizeUrl("ftp://x.example")).toThrow(CliError);
		expect(() => normalizeUrl("file:///etc/passwd")).toThrow(CliError);
		expect(() => normalizeUrl("https://user:pw@x.example")).toThrow(/credentials/);
		expect(() => normalizeUrl("not a url")).toThrow(CliError);
	});

	it("uses exit code 2 (usage)", () => {
		try {
			normalizeUrl("http://evil.example");
		} catch (error) {
			expect((error as CliError).exitCode).toBe(2);
		}
	});
});
