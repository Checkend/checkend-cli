/** Everything the CLI uses from the outside world. Tests give a fake one. */
export interface Writer {
	isTTY: boolean;
	columns: number;
	write(text: string): void;
}

export interface Context {
	env: Record<string, string | undefined>;
	cwd: string;
	homeDir: string;
	stdout: Writer;
	stderr: Writer;
	/** True when stdin and stdout are terminals, so the CLI can ask questions. */
	interactive: boolean;
	prompt(question: string, options?: { secret?: boolean }): Promise<string>;
	readStdin(): Promise<string>;
	fetch: typeof fetch;
	now(): Date;
}
