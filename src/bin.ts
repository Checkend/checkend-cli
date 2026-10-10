#!/usr/bin/env node
// The `ce` and `checkend` commands. All logic is in cli.ts; this file connects it to the
// process: terminal, environment, prompts and exit code.
import { homedir } from "node:os";
import { createInterface } from "node:readline/promises";
import { run } from "./cli.ts";
import type { Context, Writer } from "./context.ts";

const MAX_STDIN = 64 * 1024;

function writer(stream: NodeJS.WriteStream): Writer {
	return {
		isTTY: Boolean(stream.isTTY),
		columns: stream.columns || 100,
		write: (text) => {
			stream.write(text);
		},
	};
}

async function prompt(question: string, options?: { secret?: boolean }): Promise<string> {
	if (options?.secret) return promptSecret(question);
	const rl = createInterface({ input: process.stdin, output: process.stderr });
	try {
		return await rl.question(question);
	} finally {
		rl.close();
	}
}

/** Reads a line without echo. */
function promptSecret(question: string): Promise<string> {
	const stdin = process.stdin;
	process.stderr.write(question);
	stdin.setRawMode(true);
	stdin.resume();
	stdin.setEncoding("utf8");
	return new Promise((resolve) => {
		let value = "";
		const done = (result: string) => {
			stdin.setRawMode(false);
			stdin.pause();
			stdin.off("data", onData);
			process.stderr.write("\n");
			resolve(result);
		};
		const onData = (chunk: string) => {
			for (const char of chunk) {
				if (char === "\r" || char === "\n" || char === "\u{4}") return done(value);
				if (char === "\u{3}") {
					stdin.setRawMode(false);
					process.stderr.write("\n");
					process.exit(130);
				}
				if (char === "\u{7f}" || char === "\b") value = value.slice(0, -1);
				else if (char >= " ") value += char;
			}
		};
		stdin.on("data", onData);
	});
}

async function readStdin(): Promise<string> {
	let text = "";
	process.stdin.setEncoding("utf8");
	for await (const chunk of process.stdin) {
		text += chunk;
		if (text.length > MAX_STDIN) break;
	}
	return text.slice(0, MAX_STDIN);
}

// `ce issue list | head` closes stdout early; that is not an error.
process.stdout.on("error", (error: NodeJS.ErrnoException) => {
	if (error.code === "EPIPE") process.exit(process.exitCode ?? 0);
	throw error;
});

const context: Context = {
	env: process.env,
	cwd: process.cwd(),
	homeDir: homedir(),
	stdout: writer(process.stdout),
	stderr: writer(process.stderr),
	interactive: Boolean(process.stdin.isTTY && process.stdout.isTTY),
	prompt,
	readStdin,
	fetch: globalThis.fetch,
	now: () => new Date(),
};

process.exitCode = await run(process.argv.slice(2), context);
