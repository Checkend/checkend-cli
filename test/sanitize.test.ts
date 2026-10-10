import { describe, expect, it } from "vitest";
import { cleanDeep, cleanText, safeJson } from "../src/sanitize.ts";

describe("cleanText", () => {
	it("removes CSI, OSC and other escape sequences", () => {
		expect(cleanText("a\x1b[31mred\x1b[0m b")).toBe("ared b");
		expect(cleanText("x\x1b]0;evil title\x07y")).toBe("xy");
		expect(cleanText("x\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\y")).toBe("xlinky");
		expect(cleanText("x\x1bPq#0;2;0;0;0\x1b\\y")).toBe("xy");
		expect(cleanText("x\x1bcy")).toBe("xy");
	});

	it("removes C0 and C1 control characters but keeps newlines and tabs", () => {
		expect(cleanText("a\x00b\x07c\x08d\re\x7ff")).toBe("abcdef");
		expect(cleanText("a\u009b31mb\u0085c")).toBe("a31mbc");
		expect(cleanText("line1\nline2\tx")).toBe("line1\nline2\tx");
	});

	it("removes bidirectional override characters", () => {
		expect(cleanText("a\u{202e}b\u{2066}c\u{2069}d")).toBe("abcd");
	});

	it("can fold newlines and tabs into spaces for one-line cells", () => {
		expect(cleanText("a\nb\tc", { oneLine: true })).toBe("a b c");
	});

	it("handles a lone escape at the end", () => {
		expect(cleanText("abc\x1b")).toBe("abc");
	});
});

describe("cleanDeep", () => {
	it("cleans every string and key in a JSON value", () => {
		expect(cleanDeep({ "k\x1b[1m": ["v\x1b]0;t\x07", 1, null, { x: "\u009bq" }], n: 2 })).toEqual({
			k: ["v", 1, null, { x: "q" }],
			n: 2,
		});
	});
});

describe("safeJson", () => {
	it("keeps the value but escapes every control and bidi character", () => {
		const value = { m: "a\x1b[31mb\u009bc\u{202e}d\nok" };
		const text = safeJson(value);
		expect(JSON.parse(text)).toEqual(value);
		expect(text).not.toMatch(/[\x00-\x09\x0b-\x1f\x7f-\x9f\u{202a}-\u{202e}\u{2066}-\u{2069}]/u);
	});
});
