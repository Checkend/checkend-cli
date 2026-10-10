import { Api, type Health } from "../api.ts";
import type { Command, Project, Session } from "../command.ts";
import { readUserConfig, resolveToken, writeUserConfig } from "../config.ts";
import { CliError, EXIT, usageError } from "../errors.ts";
import { normalizeUrl } from "../url.ts";

const login: Command = {
	path: ["login"],
	summary: "Log in to a Checkend instance and save the token",
	usage: "[--url <url>] [--with-token]",
	details: [
		"  --url <url>      The instance, for example https://checkend.example.com",
		"  --with-token     Read the token from stdin (for scripts and CI)",
	],
	options: { "with-token": { type: "boolean" } },
	async run(session) {
		const { context, out } = session;
		let input = session.string("url") ?? context.env.CHECKEND_URL;
		if (!input && context.interactive) input = await context.prompt("Checkend URL: ");
		if (!input) {
			throw usageError("Give the URL of your Checkend instance.", "Run `ce login --url <url>`.");
		}
		const url = normalizeUrl(input);

		let token = session.string("token");
		if (session.values["with-token"]) token = await context.readStdin();
		else if (!token && context.interactive) {
			token = await context.prompt("API token: ", { secret: true });
		}
		token = token?.trim();
		if (!token) {
			throw usageError(
				"Give the API token.",
				`Pipe it on stdin: echo "$CHECKEND_TOKEN" | ce login --url ${url} --with-token`,
			);
		}
		out.hide(token);

		const api = new Api(context, url, token);
		const health = await api.health();
		if (health.status !== "ok") {
			throw new CliError(`The Checkend instance at ${url} is not healthy (${health.status}).`);
		}
		try {
			await api.get("/v1/projects");
		} catch (error) {
			if (error instanceof CliError && error.exitCode === EXIT.auth) {
				throw new CliError(
					`${url} rejected the token.`,
					EXIT.auth,
					"Use the ADMIN_TOKEN secret of your Checkend Worker.",
				);
			}
			throw error;
		}

		const config = readUserConfig(context);
		writeUserConfig(context, { current: url, hosts: { ...config.hosts, [url]: { token } } });
		const version = typeof health.version === "string" ? health.version : null;
		out.data({ url, version }, (d) => `Logged in to ${d.url} (Checkend ${d.version ?? "?"}).`);
		return EXIT.ok;
	},
};

const logout: Command = {
	path: ["logout"],
	summary: "Remove the saved token of the current instance",
	usage: "[--url <url>]",
	async run(session) {
		const { context, out } = session;
		const config = readUserConfig(context);
		const input = session.string("url") ?? config.current;
		const url = input ? normalizeUrl(input) : undefined;
		if (!url || !config.hosts[url]) {
			out.data({ url: url ?? null, logged_out: false }, () => "Not logged in.");
			return EXIT.ok;
		}
		const { [url]: _, ...hosts } = config.hosts;
		const current = config.current === url ? undefined : config.current;
		writeUserConfig(context, current ? { current, hosts } : { hosts });
		out.data({ url, logged_out: true }, (d) => `Logged out of ${d.url}.`);
		return EXIT.ok;
	},
};

interface Status {
	url: string;
	authenticated: boolean;
	health: Health;
	project:
		| { id: number | string; slug: string; name: string }
		| { link: string; found: false }
		| null;
}

const status: Command = {
	path: ["status"],
	summary: "Show the instance, its health and the linked project",
	usage: "[--project <id|slug>]",
	options: { project: { type: "string" } },
	async run(session) {
		const { context, out } = session;
		const url = session.url();
		const token = resolveToken(context, session.flags, url);
		out.hide(token);
		const api = new Api(context, url, token);
		const health = await api.health();

		let projects: Project[] | undefined;
		if (token) {
			try {
				projects = (await api.get<{ data: Project[] }>("/v1/projects")).data;
			} catch (error) {
				if (!(error instanceof CliError && error.exitCode === EXIT.auth)) throw error;
			}
		}
		const ref = session.projectRef();
		let project: Status["project"] = null;
		if (ref && projects) {
			const found = projects.find(
				(p) => String(p.id) === ref || p.slug.toLowerCase() === ref.toLowerCase(),
			);
			project = found
				? { id: found.id, slug: found.slug, name: found.name }
				: { link: ref, found: false };
		}

		const result: Status = { url, authenticated: projects !== undefined, health, project };
		out.data(result, (s) => render(session, s, ref));
		if (s(health.status) !== "ok") return EXIT.error;
		if (!result.authenticated) {
			out.note(
				token
					? `The token was rejected. Run \`ce login --url ${url}\`.`
					: `Not logged in to ${url}. Run \`ce login --url ${url}\`.`,
			);
			return EXIT.auth;
		}
		return EXIT.ok;
	},
};

const s = (value: unknown) => (value === undefined || value === null ? "-" : String(value));

function render(session: Session, status: Status, ref: string | undefined): string {
	const { out } = session;
	const { health, project } = status;
	const healthy = health.status === "ok";
	const mode = s(health.ingest?.mode);
	let linked = "none (run `ce init`)";
	if (project && "slug" in project) linked = `${project.slug} (${project.id})`;
	else if (project) linked = out.red(`${project.link} (not found)`);
	else if (ref) linked = `${ref} (log in to check it)`;
	return out.fields([
		["Instance", status.url],
		["Version", s(health.version)],
		["Health", healthy ? out.green("ok") : out.red(s(health.status))],
		["Ingest", mode === "full" ? mode : out.yellow(mode)],
		["Logged in", status.authenticated ? "yes" : out.red("no")],
		["Project", linked],
	]);
}

export const authCommands: Command[] = [login, logout, status];
