import type { ParseArgsOptionsConfig } from "node:util";
import { Api } from "./api.ts";
import {
	findLink,
	type GlobalFlags,
	type Link,
	readUserConfig,
	resolveToken,
	resolveUrl,
} from "./config.ts";
import type { Context } from "./context.ts";
import { CliError, EXIT, usageError } from "./errors.ts";
import type { Output } from "./output.ts";

export type Values = Record<string, string | boolean | undefined>;

export interface Command {
	/** For example ["issue", "list"]. */
	path: string[];
	summary: string;
	/** Arguments and options, for the help. For example "<id> [--section <name>]". */
	usage: string;
	/** Option lines for the help. */
	details?: string[];
	options?: ParseArgsOptionsConfig;
	/** Names of the positional arguments. A name in brackets is optional: "[project]". */
	args?: string[];
	run(session: Session): Promise<number | undefined>;
}

export interface Project {
	id: number | string;
	slug: string;
	name: string;
	platform: string | null;
	created_at?: string;
	dsns?: string[];
	[key: string]: unknown;
}

/** One run of a command: parsed arguments, output and lazy access to the API. */
export class Session {
	readonly context: Context;
	readonly out: Output;
	readonly values: Values;
	readonly args: string[];

	constructor(context: Context, out: Output, values: Values, args: string[]) {
		this.context = context;
		this.out = out;
		this.values = values;
		this.args = args;
	}

	get flags(): GlobalFlags {
		return { url: this.string("url"), token: this.string("token") };
	}

	string(name: string): string | undefined {
		const value = this.values[name];
		return typeof value === "string" ? value : undefined;
	}

	/** The instance URL, or exit 5 with the next step. */
	url(): string {
		const url = resolveUrl(this.context, this.flags);
		if (!url) throw new CliError("Not logged in.", EXIT.auth, "Run `ce login`.");
		return url;
	}

	/** An API client with a token, or exit 5 with the next step. */
	api(): Api {
		const url = this.url();
		const token = resolveToken(this.context, this.flags, url);
		if (!token) {
			const saved = Object.keys(readUserConfig(this.context).hosts).length > 0;
			throw new CliError(
				saved ? `Not logged in to ${url}.` : "Not logged in.",
				EXIT.auth,
				`Run \`ce login --url ${url}\`.`,
			);
		}
		this.out.hide(token);
		return new Api(this.context, url, token);
	}

	link(): Link | undefined {
		return findLink(this.context);
	}

	/** The project from --project or .checkend.json, or undefined. */
	projectRef(): string | undefined {
		return this.string("project") ?? this.link()?.project;
	}

	/**
	 * The project for a `?project=` filter. The server takes the public ID or the slug
	 * (lowercase), so no lookup is needed. An unknown project gives an empty list.
	 */
	projectFilter(): string | undefined {
		return this.projectRef()?.trim().toLowerCase() || undefined;
	}

	/** Finds a project by public ID or slug. */
	async project(api: Api, ref: string): Promise<Project> {
		const { data } = await api.get<{ data: Project[] }>("/v1/projects");
		const wanted = ref.trim().toLowerCase();
		const project = data.find((p) => String(p.id) === wanted || p.slug.toLowerCase() === wanted);
		if (!project) {
			throw new CliError(
				`Project "${ref}" not found.`,
				EXIT.notFound,
				"Run `ce project list` to see the projects.",
			);
		}
		return project;
	}
}

/** Parses a positive integer ID, or exits 2. */
export function parseId(value: string | undefined, what: string): number {
	if (!value || !/^\d{1,15}$/.test(value)) throw usageError(`${what} must be a number.`);
	return Number(value);
}
