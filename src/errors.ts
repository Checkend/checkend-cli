/** Exit codes. Scripts and agents depend on them: do not change a value. */
export const EXIT = {
	ok: 0,
	error: 1,
	usage: 2,
	notFound: 3,
	conflict: 4,
	auth: 5,
} as const;

export type ExitCode = (typeof EXIT)[keyof typeof EXIT];

/** An error with an exit code and an optional next step for the user. */
export class CliError extends Error {
	readonly exitCode: ExitCode;
	readonly hint: string | undefined;

	constructor(message: string, exitCode: ExitCode = EXIT.error, hint?: string) {
		super(message);
		this.name = "CliError";
		this.exitCode = exitCode;
		this.hint = hint;
	}
}

export const usageError = (message: string, hint = "Run `ce --help` to see the commands.") =>
	new CliError(message, EXIT.usage, hint);
