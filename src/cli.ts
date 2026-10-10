import { type ParseArgsOptionsConfig, parseArgs } from "node:util";
import { type Command, Session, type Values } from "./command.ts";
import { authCommands } from "./commands/auth.ts";
import { init } from "./commands/init.ts";
import { projectCommands } from "./commands/project.ts";
import type { Context } from "./context.ts";
import { CliError, EXIT, usageError } from "./errors.ts";
import { Output } from "./output.ts";
import { cleanText } from "./sanitize.ts";
import { VERSION } from "./version.ts";

const GLOBAL_OPTIONS: ParseArgsOptionsConfig = {
	url: { type: "string" },
	token: { type: "string" },
	json: { type: "boolean" },
	help: { type: "boolean", short: "h" },
};

// Only commands whose server API exists are listed here (and so in --help).
const COMMANDS: Command[] = [...authCommands, init, ...projectCommands];

const GROUPS: Record<string, string> = {
	project: "Create and list projects; print a DSN",
};

export async function run(argv: string[], context: Context): Promise<number> {
	const out = new Output(context, { json: argv.includes("--json") });
	out.hide(context.env.CHECKEND_TOKEN);
	out.hide(context.env.CHECKEND_ADMIN_TOKEN);
	argv.forEach((arg, i) => {
		if (arg === "--token") out.hide(argv[i + 1]);
		if (arg.startsWith("--token=")) out.hide(arg.slice("--token=".length));
	});

	try {
		return await dispatch(argv, context, out);
	} catch (error) {
		if (error instanceof CliError) {
			out.error(error);
			return error.exitCode;
		}
		const message = error instanceof Error ? error.message : String(error);
		out.error(new CliError(`Unexpected error: ${message}`, EXIT.error));
		return EXIT.error;
	}
}

async function dispatch(argv: string[], context: Context, out: Output): Promise<number> {
	const [first, second] = argv;
	if (first === undefined || (argv.length === 1 && isHelp(first)) || first === "help") {
		if (first === "help" && argv.length > 1)
			return dispatch([...argv.slice(1), "--help"], context, out);
		out.write(mainHelp());
		return EXIT.ok;
	}
	if (argv.length === 1 && (first === "--version" || first === "-V")) {
		out.write(`${VERSION}\n`);
		return EXIT.ok;
	}
	if (first.startsWith("-")) throw usageError("Give a command before the options.");

	const group = COMMANDS.filter((c) => c.path[0] === first);
	if (group.length === 0)
		throw usageError(`Unknown command "${cleanText(first, { oneLine: true })}".`);

	let command = group.find((c) => c.path.length === 1);
	let rest = argv.slice(1);
	if (!command) {
		if (second === undefined || isHelp(second)) {
			if (second === undefined) {
				throw usageError(`"ce ${first}" needs a subcommand.`, `Run \`ce ${first} --help\`.`);
			}
			out.write(groupHelp(first, group));
			return EXIT.ok;
		}
		command = group.find((c) => c.path[1] === second);
		if (!command) {
			throw usageError(
				`Unknown command "ce ${first} ${cleanText(second, { oneLine: true })}".`,
				`Run \`ce ${first} --help\`.`,
			);
		}
		rest = argv.slice(2);
	}

	let values: Values;
	let positionals: string[];
	try {
		({ values, positionals } = parseArgs({
			args: rest,
			options: { ...GLOBAL_OPTIONS, ...command.options },
			allowPositionals: true,
			strict: true,
		}) as { values: Values; positionals: string[] });
	} catch (error) {
		const message = error instanceof Error ? error.message.split("\n")[0] : String(error);
		throw usageError(cleanText(message ?? "", { oneLine: true }), helpHint(command));
	}
	if (values.help) {
		out.write(commandHelp(command));
		return EXIT.ok;
	}
	const names = command.args ?? [];
	const required = names.filter((name) => !name.startsWith("[")).length;
	if (positionals.length < required) {
		throw usageError(`Missing ${names[positionals.length]}.`, helpHint(command));
	}
	if (positionals.length > names.length) {
		const extra = cleanText(positionals[names.length] ?? "", { oneLine: true });
		throw usageError(`Unexpected argument "${extra}".`, helpHint(command));
	}
	return (await command.run(new Session(context, out, values, positionals))) ?? EXIT.ok;
}

const isHelp = (arg: string) => arg === "--help" || arg === "-h";

const name = (command: Command) => `ce ${command.path.join(" ")}`;

const helpHint = (command: Command) => `Run \`${name(command)} --help\`.`;

function mainHelp(): string {
	const seen = new Set<string>();
	const lines: string[] = [];
	for (const command of COMMANDS) {
		const [head] = command.path;
		if (!head || seen.has(head)) continue;
		seen.add(head);
		const summary = GROUPS[head] ?? command.summary;
		const label = command.path.length > 1 ? `${head} <action>` : head;
		lines.push(`  ${label.padEnd(18)}${summary}`);
	}
	return `Checkend CLI ${VERSION}: error tracking for coding agents.

Usage: ce <command> [options]

Commands:
${lines.join("\n")}

Options for all commands:
  --url <url>       The Checkend instance (or CHECKEND_URL)
  --token <token>   The API token (or CHECKEND_TOKEN)
  --json            Print JSON (the default when stdout is not a terminal)
  -h, --help        Show the help
  -V, --version     Show the version

Run \`ce <command> --help\` for the options of a command.
Exit codes: 0 OK, 1 error, 2 usage, 3 not found, 4 conflict, 5 authentication.
`;
}

function groupHelp(head: string, group: Command[]): string {
	const lines = group.map((c) => `  ${`${name(c)} ${c.usage}`.trimEnd()}\n      ${c.summary}`);
	return `${GROUPS[head] ?? ""}\n\nUsage:\n${lines.join("\n")}\n`;
}

function commandHelp(command: Command): string {
	const details = command.details?.length ? `\n\nOptions:\n${command.details.join("\n")}` : "";
	return `${command.summary}\n\nUsage: ${`${name(command)} ${command.usage}`.trimEnd()}${details}\n`;
}
