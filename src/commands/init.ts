import { writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import type { Command, Project, Session } from "../command.ts";
import { findRepoRoot, LINK_FILE } from "../config.ts";
import { EXIT, usageError } from "../errors.ts";
import { checkDsn, detectPlatform, snippet } from "../snippets.ts";

interface Result {
	project: { id: number | string; slug: string; name: string };
	created: boolean;
	dsn: string;
	platform: string | null;
	file: string;
	snippet: string;
}

export const init: Command = {
	path: ["init"],
	summary: "Link this repo to a project (or create one) and print the SDK setup",
	usage: "[--name <name> | --project <id|slug>] [--platform <platform>]",
	details: [
		"  --name <name>          Create a project with this name",
		"  --project <id|slug>    Link an existing project",
		"  --platform <platform>  Override the detected platform",
		"",
		"Writes .checkend.json (the project link only, never a token; safe to commit).",
	],
	options: {
		name: { type: "string" },
		project: { type: "string" },
		platform: { type: "string" },
	},
	async run(session) {
		const { context, out } = session;
		const root = findRepoRoot(context.cwd) ?? context.cwd;
		const name = session.string("name");
		const ref = session.string("project") ?? (name ? undefined : session.link()?.project);
		if (!name && !ref && !context.interactive) {
			throw usageError(
				"Give a project.",
				'Use --name "My App" to create a project or --project <id|slug> to link one.',
			);
		}
		const api = session.api();
		const platform = session.string("platform") ?? detectPlatform(root);

		let project: Project;
		let dsn: unknown;
		let created = false;
		if (ref) {
			project = await session.project(api, ref);
			dsn = project.dsns?.[0];
		} else {
			const choice = name ?? (await choose(session, basename(root)));
			if (typeof choice === "string") {
				const body = await api.post<{ data: Project & { dsn: string } }>("/v1/projects", {
					name: choice,
					...(platform ? { platform } : {}),
				});
				project = body.data;
				dsn = body.data.dsn;
				created = true;
			} else {
				project = choice;
				dsn = project.dsns?.[0];
			}
		}

		const checked = checkDsn(dsn);
		const file = join(root, LINK_FILE);
		writeFileSync(
			file,
			`${JSON.stringify({ project: String(project.id), slug: project.slug }, null, 2)}\n`,
		);

		const result: Result = {
			project: { id: project.id, slug: project.slug, name: project.name },
			created,
			dsn: checked,
			platform: platform ?? null,
			file,
			snippet: snippet(platform, checked),
		};
		out.data(result, (r) =>
			[
				`${r.created ? "Created" : "Linked"} project ${r.project.slug} (${r.project.id}).`,
				`Wrote ${r.file} (the project link only, no token; safe to commit).`,
				"",
				`DSN: ${r.dsn}`,
				"",
				"Set up the Sentry SDK in your app (Checkend is Sentry SDK compatible):",
				"",
				r.snippet.replace(/^/gm, "    "),
				"",
				"Next: run `ce status`. When errors arrive, run `ce next`.",
			].join("\n"),
		);
		return EXIT.ok;
	},
};

/** Asks which project to link. Returns a project, or the name for a new one. */
async function choose(session: Session, defaultName: string): Promise<Project | string> {
	const { out, context } = session;
	const { data } = await session.api().get<{ data: Project[] }>("/v1/projects");
	const lines = data.map((p, i) => `  ${i + 1}) ${p.slug} (${p.id})`);
	out.menu([
		...(lines.length ? ["Projects:", ...lines] : []),
		`  n) Create a new project "${defaultName}"`,
	]);
	const answer = (await context.prompt("Choose a number, or press Enter for n: ")).trim();
	if (answer === "" || answer.toLowerCase() === "n") return defaultName;
	const index = /^\d+$/.test(answer) ? Number(answer) - 1 : -1;
	const project = data[index];
	if (!project) throw usageError(`"${answer}" is not a choice.`, "Run `ce init` again.");
	return project;
}
