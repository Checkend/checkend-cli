// Output modes: short tables and cards on a terminal; the API JSON when stdout is not a
// terminal or with --json. All server text is cleaned before it is printed, and known
// secrets are hidden in all output.
import type { Context } from "./context.ts";
import type { CliError } from "./errors.ts";
import { cleanDeep, cleanText, safeJson } from "./sanitize.ts";

const HIDDEN = "[hidden]";

export class Output {
	readonly json: boolean;
	readonly color: boolean;
	readonly columns: number;
	private readonly context: Context;
	private readonly secrets = new Set<string>();

	constructor(context: Context, options: { json: boolean }) {
		this.context = context;
		this.json = options.json || !context.stdout.isTTY;
		const noColor = context.env.NO_COLOR !== undefined && context.env.NO_COLOR !== "";
		this.color = !this.json && context.stdout.isTTY && !noColor;
		this.columns = context.stdout.columns || 100;
	}

	/** Hides this value in everything that the CLI prints. */
	hide(secret: string | undefined): void {
		if (secret && secret.length >= 4) this.secrets.add(secret);
	}

	/** Prints the value as JSON, or the text from `render` (which gets a cleaned copy). */
	data<T>(value: T, render: (clean: T) => string): void {
		if (this.json) this.write(`${safeJson(value)}\n`);
		else this.write(ensureNewline(render(cleanDeep(value))));
	}

	/** A message for people. It is not printed in JSON mode. */
	info(text: string): void {
		if (!this.json) this.write(ensureNewline(text));
	}

	/** A note on stderr, on a terminal only. */
	note(text: string): void {
		if (!this.json) this.context.stderr.write(this.redact(ensureNewline(text)));
	}

	/** Lines for a question, on stderr in all modes. Server text in them is cleaned. */
	menu(lines: string[]): void {
		const text = lines.map((line) => cleanText(line, { oneLine: true })).join("\n");
		this.context.stderr.write(this.redact(`${text}\n`));
	}

	error(error: CliError): void {
		const message = cleanText(error.message);
		const hint = error.hint === undefined ? undefined : cleanText(error.hint);
		const text = this.json
			? `${safeJson(hint === undefined ? { error: message } : { error: message, hint })}\n`
			: `${this.paint("31", "Error:")} ${message}\n${hint ? `${hint}\n` : ""}`;
		this.context.stderr.write(this.redact(text));
	}

	write(text: string): void {
		this.context.stdout.write(this.redact(text));
	}

	bold = (text: string) => this.paint("1", text);
	dim = (text: string) => this.paint("2", text);
	red = (text: string) => this.paint("31", text);
	green = (text: string) => this.paint("32", text);
	yellow = (text: string) => this.paint("33", text);

	level(level: unknown): string {
		const text = String(level ?? "");
		if (text === "fatal" || text === "error") return this.red(text);
		if (text === "warning") return this.yellow(text);
		return text;
	}

	/** A table. Cells are one line; the last column is cut to fit the terminal. */
	table(headers: string[], rows: unknown[][]): string {
		const cells = rows.map((row) =>
			row.map((cell) => cleanText(String(cell ?? ""), { oneLine: true })),
		);
		const widths = headers.map((header, i) =>
			Math.max(header.length, ...cells.map((row) => (row[i] ?? "").length)),
		);
		const last = headers.length - 1;
		const fixed = widths.slice(0, last).reduce((sum, width) => sum + width + 2, 0);
		widths[last] = Math.max(10, Math.min(widths[last] ?? 0, this.columns - fixed));
		const line = (row: string[], paint: (text: string) => string = (t) => t) =>
			row
				.map((cell, i) => {
					const width = widths[i] ?? 0;
					const text = cell.length > width ? `${cell.slice(0, width - 1)}…` : cell;
					return i === last ? paint(text) : paint(text.padEnd(width));
				})
				.join("  ")
				.trimEnd();
		return [line(headers, this.bold), ...cells.map((row) => line(row))].join("\n");
	}

	/** Label/value lines. Rows with an undefined value are left out. */
	fields(rows: [string, unknown][]): string {
		const shown = rows.filter(([, value]) => value !== undefined);
		const width = Math.max(...shown.map(([label]) => label.length));
		return shown
			.map(([label, value]) => `${this.dim(label.padEnd(width))}  ${String(value ?? "-")}`)
			.join("\n");
	}

	private paint(code: string, text: string): string {
		return this.color ? `\x1b[${code}m${text}\x1b[0m` : text;
	}

	private redact(text: string): string {
		let result = text;
		for (const secret of this.secrets) result = result.split(secret).join(HIDDEN);
		return result;
	}
}

const ensureNewline = (text: string) => (text.endsWith("\n") ? text : `${text}\n`);

/** "5m ago", "3h ago", "2d ago". */
export function ago(iso: unknown, now: Date): string {
	const time = Date.parse(String(iso));
	if (Number.isNaN(time)) return "-";
	const seconds = Math.max(0, Math.round((now.getTime() - time) / 1000));
	if (seconds < 60) return `${seconds}s ago`;
	if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
	if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
	return `${Math.floor(seconds / 86400)}d ago`;
}
