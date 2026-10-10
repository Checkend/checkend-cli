import { usageError } from "./errors.ts";

const LOCAL_HOSTS = new Set(["localhost", "127.0.0.1"]);

/**
 * Checks and normalizes the URL of a Checkend instance. HTTPS only, except for
 * http://localhost and http://127.0.0.1, because the token goes in each request.
 */
export function normalizeUrl(input: string): string {
	let url: URL;
	try {
		url = new URL(input.trim());
	} catch {
		throw usageError(`"${input}" is not a valid URL.`, "Example: https://checkend.example.com");
	}
	if (url.protocol !== "https:" && !(url.protocol === "http:" && LOCAL_HOSTS.has(url.hostname))) {
		throw usageError(
			`Use HTTPS for ${url.protocol}//${url.host}.`,
			"Only http://localhost and http://127.0.0.1 can use plain HTTP.",
		);
	}
	if (url.username || url.password) {
		throw usageError("Do not put credentials in the URL.", "Use `ce login` to save the token.");
	}
	return `${url.origin}${url.pathname.replace(/\/+$/, "")}`;
}
