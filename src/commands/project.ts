import type { Command, Project } from "../command.ts";
import { EXIT, usageError } from "../errors.ts";

const list: Command = {
	path: ["project", "list"],
	summary: "List the projects",
	usage: "",
	async run(session) {
		const body = await session.api().get<{ data: Project[] }>("/v1/projects");
		session.out.data(body, ({ data }) =>
			data.length === 0
				? 'No projects yet. Run `ce project create --name "My App"` or `ce init`.'
				: session.out.table(
						["ID", "SLUG", "NAME", "PLATFORM"],
						data.map((p) => [p.id, p.slug, p.name, p.platform ?? "-"]),
					),
		);
		return EXIT.ok;
	},
};

const create: Command = {
	path: ["project", "create"],
	summary: "Create a project and print its DSN",
	usage: "--name <name> [--platform <platform>]",
	details: [
		"  --name <name>          1 to 100 characters",
		"  --platform <platform>  For example node, javascript, python, ruby, go, php",
	],
	options: { name: { type: "string" }, platform: { type: "string" } },
	async run(session) {
		const name = session.string("name");
		if (name === undefined)
			throw usageError("Missing --name.", 'Example: ce project create --name "My App"');
		const platform = session.string("platform");
		const body = await session.api().post<{ data: Project & { dsn: string } }>("/v1/projects", {
			name,
			...(platform ? { platform } : {}),
		});
		session.out.data(body, ({ data }) =>
			[
				`Created project ${data.slug} (${data.id}).`,
				`DSN: ${data.dsn}`,
				"Run `ce init` in the repo of the app to link it and get the SDK snippet.",
			].join("\n"),
		);
		return EXIT.ok;
	},
};

const dsn: Command = {
	path: ["project", "dsn"],
	summary: "Print the DSN of a project (default: the linked project)",
	usage: "[<id|slug>] [--project <id|slug>]",
	options: { project: { type: "string" } },
	args: ["[project]"],
	async run(session) {
		const ref = session.args[0] ?? session.projectRef();
		if (!ref) {
			throw usageError(
				"No project given and no .checkend.json found.",
				"Run `ce init` to link this repo, or give a project.",
			);
		}
		const project = await session.project(session.api(), ref);
		const dsns = project.dsns ?? [];
		session.out.data({ data: { id: project.id, slug: project.slug, dsns } }, ({ data }) =>
			data.dsns.length ? data.dsns.join("\n") : `Project ${data.slug} has no DSN.`,
		);
		return EXIT.ok;
	},
};

export const projectCommands: Command[] = [list, create, dsn];
