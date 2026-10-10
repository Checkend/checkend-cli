// Runs the CLI in-process with a fake terminal, a temporary home and a temporary repo.
import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { run } from "../../src/cli.ts";
import type { Context } from "../../src/context.ts";

export interface CliResult {
	code: number;
	stdout: string;
	stderr: string;
	/** stdout and stderr together. */
	all: string;
}

export interface HarnessOptions {
	env?: Record<string, string | undefined>;
	tty?: boolean;
	/** Answers for prompts, in order. */
	answers?: string[];
	stdin?: string;
	cwd?: string;
}

export class Harness {
	readonly home: string;
	readonly repo: string;
	readonly prompts: { question: string; secret: boolean }[] = [];

	constructor() {
		const root = mkdtempSync(join(tmpdir(), "checkend-cli-test-"));
		this.home = join(root, "home");
		this.repo = join(root, "repo");
		mkdirSync(this.home);
		mkdirSync(join(this.repo, ".git"), { recursive: true });
	}

	async run(argv: string[], options: HarnessOptions = {}): Promise<CliResult> {
		let stdout = "";
		let stderr = "";
		let all = "";
		const answers = [...(options.answers ?? [])];
		const tty = options.tty ?? false;
		const context: Context = {
			env: { HOME: this.home, NO_COLOR: "1", ...options.env },
			cwd: options.cwd ?? this.repo,
			homeDir: this.home,
			stdout: {
				isTTY: tty,
				columns: 100,
				write: (text) => {
					stdout += text;
					all += text;
				},
			},
			stderr: {
				isTTY: tty,
				columns: 100,
				write: (text) => {
					stderr += text;
					all += text;
				},
			},
			interactive: tty,
			prompt: async (question, promptOptions) => {
				this.prompts.push({ question, secret: promptOptions?.secret ?? false });
				const answer = answers.shift();
				if (answer === undefined) throw new Error(`No answer for prompt: ${question}`);
				return answer;
			},
			readStdin: async () => options.stdin ?? "",
			fetch: globalThis.fetch,
			now: () => new Date("2026-10-10T12:00:00.000Z"),
		};
		const code = await run(argv, context);
		return { code, stdout, stderr, all };
	}
}
