// The HTTP client for the Checkend API. It maps HTTP errors to exit codes.
import type { Context } from "./context.ts";
import { CliError, EXIT } from "./errors.ts";
import { cleanText } from "./sanitize.ts";
import { VERSION } from "./version.ts";

const TIMEOUT_MS = 30_000;

export interface Health {
	status: string;
	version?: string;
	ingest?: { mode?: string } | null;
	[key: string]: unknown;
}

export class Api {
	readonly url: string;
	private readonly token: string | undefined;
	private readonly context: Context;

	constructor(context: Context, url: string, token: string | undefined) {
		this.context = context;
		this.url = url;
		this.token = token;
	}

	/** GET /health. It needs no token. A 503 still has a health body. */
	async health(): Promise<Health> {
		const response = await this.send("GET", "/health", undefined, false);
		const body = await this.json(response);
		const health = body as Health | null;
		if (typeof health?.status !== "string") {
			throw new CliError(
				`The server at ${this.url} is not a Checkend instance.`,
				EXIT.error,
				"Check the URL.",
			);
		}
		return health;
	}

	async get<T>(path: string, query: Record<string, string | undefined> = {}): Promise<T> {
		const params = new URLSearchParams();
		for (const [key, value] of Object.entries(query)) {
			if (value !== undefined) params.set(key, value);
		}
		const search = params.size ? `?${params}` : "";
		return this.call<T>("GET", `${path}${search}`);
	}

	async post<T>(path: string, body: unknown): Promise<T> {
		return this.call<T>("POST", path, body);
	}

	private async call<T>(method: string, path: string, body?: unknown): Promise<T> {
		const response = await this.send(method, path, body, true);
		const json = await this.json(response);
		if (response.ok) return json as T;
		throw this.httpError(response, json);
	}

	private async send(
		method: string,
		path: string,
		body: unknown,
		auth: boolean,
	): Promise<Response> {
		const headers: Record<string, string> = {
			accept: "application/json",
			"user-agent": `checkend-cli/${VERSION}`,
		};
		if (auth && this.token) headers.authorization = `Bearer ${this.token}`;
		if (body !== undefined) headers["content-type"] = "application/json";
		let response: Response;
		try {
			response = await this.context.fetch(`${this.url}${path}`, {
				method,
				headers,
				...(body === undefined ? {} : { body: JSON.stringify(body) }),
				// A redirect could send the token to another host.
				redirect: "manual",
				signal: AbortSignal.timeout(TIMEOUT_MS),
			});
		} catch (error) {
			const cause = error instanceof Error ? (error.cause ?? error) : error;
			const reason = cause instanceof Error ? cause.message : String(cause);
			throw new CliError(
				`Cannot reach ${this.url}: ${reason}`,
				EXIT.error,
				"Check the URL and your network.",
			);
		}
		if (response.status >= 300 && response.status < 400) {
			throw new CliError(
				`${this.url} answered with a redirect (HTTP ${response.status}). The CLI does not follow redirects.`,
				EXIT.error,
				"Use the final URL of your Checkend instance.",
			);
		}
		return response;
	}

	private async json(response: Response): Promise<unknown> {
		const text = await response.text();
		try {
			return text ? JSON.parse(text) : null;
		} catch {
			throw new CliError(
				`The server at ${this.url} is not a Checkend instance (HTTP ${response.status}, not JSON).`,
				EXIT.error,
				"Check the URL.",
			);
		}
	}

	private httpError(response: Response, body: unknown): CliError {
		const error = (body as { error?: unknown } | null)?.error;
		const code = typeof error === "string" ? cleanText(error, { oneLine: true }) : "";
		switch (response.status) {
			case 400: {
				const details = (body as { details?: { path?: unknown; message?: unknown }[] } | null)
					?.details;
				const text = Array.isArray(details)
					? details.map((d) => `${String(d.path)}: ${String(d.message)}`).join("; ")
					: code;
				return new CliError(`Invalid request: ${cleanText(text, { oneLine: true })}`, EXIT.usage);
			}
			case 401:
			case 403:
				return new CliError(
					`${this.url} rejected the token.`,
					EXIT.auth,
					"Run `ce login` to save a valid token.",
				);
			case 404:
				return new CliError("Not found.", EXIT.notFound);
			case 409:
				return new CliError(`Conflict: ${code}`, EXIT.conflict);
			case 429:
				return new CliError(
					"Too many requests.",
					EXIT.error,
					`Try again in ${response.headers.get("retry-after") ?? "a few"} seconds.`,
				);
			default:
				return new CliError(
					`The server answered HTTP ${response.status}${code ? `: ${code}` : ""}.`,
					EXIT.error,
				);
		}
	}
}

/** Runs an API call; a 404 becomes the given message. */
export async function orNotFound<T>(promise: Promise<T>, message: string, hint?: string) {
	try {
		return await promise;
	} catch (error) {
		if (error instanceof CliError && error.exitCode === EXIT.notFound) {
			throw new CliError(message, EXIT.notFound, hint);
		}
		throw error;
	}
}
