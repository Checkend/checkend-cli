// Server text (event messages, frames, request data, project names) is untrusted. A terminal
// runs escape sequences, so all server text is cleaned before it is printed.

const ESCAPE_SEQUENCES = new RegExp(
	[
		"\\x1b\\[[0-?]*[ -/]*[@-~]?", // CSI
		"\\x1b\\][^\\x07\\x1b]*(?:\\x07|\\x1b\\\\)?", // OSC, ended by BEL or ST
		"\\x1b[PX^_][^\\x1b]*(?:\\x1b\\\\)?", // DCS, SOS, PM, APC
		"\\x1b[ -/]*[0-~]?", // all other escapes
	].join("|"),
	"g",
);

// C0 controls except tab and newline, DEL, C1 controls, and bidirectional controls.
const CONTROLS =
	/[\x00-\x08\x0b-\x1f\x7f-\x9f\u{61c}\u{200e}\u{200f}\u{202a}-\u{202e}\u{2066}-\u{2069}]/gu;

export function cleanText(text: string, options: { oneLine?: boolean } = {}): string {
	const clean = text.replace(ESCAPE_SEQUENCES, "").replace(CONTROLS, "");
	return options.oneLine ? clean.replace(/[\n\t]/g, " ") : clean;
}

/** Returns a copy of a JSON value with every string and object key cleaned. */
export function cleanDeep<T>(value: T): T {
	if (typeof value === "string") return cleanText(value) as T;
	if (Array.isArray(value)) return value.map(cleanDeep) as T;
	if (value !== null && typeof value === "object") {
		return Object.fromEntries(
			Object.entries(value).map(([key, item]) => [cleanText(key), cleanDeep(item)]),
		) as T;
	}
	return value;
}

// JSON.stringify escapes C0 controls but not DEL, C1 or bidirectional controls.
const JSON_UNSAFE =
	/[\x7f-\x9f\u{61c}\u{200e}\u{200f}\u{2028}\u{2029}\u{202a}-\u{202e}\u{2066}-\u{2069}]/gu;

/**
 * JSON for output. The parsed value is unchanged; characters that a terminal could act on
 * are written as \u escapes.
 */
export function safeJson(value: unknown): string {
	return JSON.stringify(value, null, 2).replace(
		JSON_UNSAFE,
		(char) => `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`,
	);
}
