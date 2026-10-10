// Configuration, strongest first: flags, environment, .checkend.json (project link only),
// then the user config from `ce login`.
import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { Context } from "./context.ts";
import { CliError, EXIT } from "./errors.ts";
import { normalizeUrl } from "./url.ts";

export const LINK_FILE = ".checkend.json";

export interface UserConfig {
	current?: string;
	hosts: Record<string, { token: string }>;
}

export interface GlobalFlags {
	url?: string | undefined;
	token?: string | undefined;
}

export function configDir(context: Context): string {
	const xdg = context.env.XDG_CONFIG_HOME;
	return xdg ? join(xdg, "checkend") : join(context.homeDir, ".config", "checkend");
}

const configFile = (context: Context) => join(configDir(context), "config.json");

export function readUserConfig(context: Context): UserConfig {
	const file = configFile(context);
	if (!existsSync(file)) return { hosts: {} };
	try {
		const value = JSON.parse(readFileSync(file, "utf8")) as Partial<UserConfig>;
		const hosts = value.hosts && typeof value.hosts === "object" ? value.hosts : {};
		return typeof value.current === "string" ? { current: value.current, hosts } : { hosts };
	} catch {
		throw new CliError(`Cannot read ${file}.`, EXIT.error, "Run `ce login` to write it again.");
	}
}

/** Writes the user config: directory mode 700, file mode 600, atomic replace. */
export function writeUserConfig(context: Context, config: UserConfig): void {
	const dir = configDir(context);
	mkdirSync(dir, { recursive: true, mode: 0o700 });
	chmodSync(dir, 0o700);
	const file = configFile(context);
	const temp = `${file}.${process.pid}.tmp`;
	writeFileSync(temp, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
	chmodSync(temp, 0o600);
	renameSync(temp, file);
}

/** The instance URL: --url, then CHECKEND_URL, then the instance of `ce login`. */
export function resolveUrl(context: Context, flags: GlobalFlags): string | undefined {
	const value = flags.url ?? context.env.CHECKEND_URL ?? readUserConfig(context).current;
	return value ? normalizeUrl(value) : undefined;
}

/**
 * The token: --token, then CHECKEND_TOKEN or CHECKEND_ADMIN_TOKEN, then the token saved for
 * this exact URL. A saved token never goes to another instance.
 */
export function resolveToken(
	context: Context,
	flags: GlobalFlags,
	url: string,
): string | undefined {
	return (
		flags.token ??
		context.env.CHECKEND_TOKEN ??
		context.env.CHECKEND_ADMIN_TOKEN ??
		readUserConfig(context).hosts[url]?.token
	);
}

export interface Link {
	file: string;
	project: string;
}

/** Finds .checkend.json in the current directory or a parent, up to the git repo root. */
export function findLink(context: Context): Link | undefined {
	let dir = context.cwd;
	for (;;) {
		const file = join(dir, LINK_FILE);
		if (existsSync(file)) return readLink(file);
		if (existsSync(join(dir, ".git"))) return undefined;
		const parent = dirname(dir);
		if (parent === dir) return undefined;
		dir = parent;
	}
}

function readLink(file: string): Link | undefined {
	let value: unknown;
	try {
		value = JSON.parse(readFileSync(file, "utf8"));
	} catch {
		throw new CliError(`Cannot read ${file}: it is not valid JSON.`, EXIT.error);
	}
	const project = (value as { project?: unknown } | null)?.project;
	if (project === undefined) return undefined;
	if (typeof project !== "string" && typeof project !== "number") {
		throw new CliError(`Cannot read ${file}: "project" must be a project ID or slug.`);
	}
	return { file, project: String(project) };
}

/** Finds the git repo root, or undefined outside a repo. */
export function findRepoRoot(start: string): string | undefined {
	let dir = start;
	for (;;) {
		if (existsSync(join(dir, ".git"))) return dir;
		const parent = dirname(dir);
		if (parent === dir) return undefined;
		dir = parent;
	}
}
