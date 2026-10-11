// Builds the cross-test fixtures for internal/sourcemap: source maps, and the index (.cksi) or the
// error (.error) that the SERVER's buildSourceMapIndex gives for each. The Go builder must give
// the same bytes and the same errors.
//
// Usage: node scripts/gen-sourcemap-fixtures.mjs <checkout of Checkend/checkend> [esbuild binary]
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const [server, esbuild] = process.argv.slice(2);
if (!server) throw new Error("Give the path of a checkout of Checkend/checkend.");
const { buildSourceMapIndex } = await import(
	pathToFileURL(resolve(server, "src/core/sourcemap-index.ts")).href
);
const out = resolve(import.meta.dirname, "../internal/sourcemap/testdata");

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
function vlq(value) {
	let v = value < 0 ? (-value) * 2 + 1 : value * 2;
	let s = "";
	do {
		let digit = v % 32;
		v = Math.floor(v / 32);
		if (v > 0) digit |= 32;
		s += B64[digit];
	} while (v > 0);
	return s;
}

// A deterministic large map: 2000 lines of 20 segments, 3 sources, 50 names.
function bigMap() {
	let seed = 42;
	const rand = (n) => {
		seed = (seed * 1103515245 + 12345) % 2147483648;
		return seed % n;
	};
	const state = [0, 0, 0, 0];
	const lines = [];
	for (let l = 0; l < 2000; l++) {
		let column = 0;
		const segs = [];
		for (let s = 0; s < 20; s++) {
			const col = rand(400);
			const fields = [vlq(col - column)];
			column = col;
			if (rand(10) > 0) {
				const src = rand(3), line = rand(500), c = rand(80);
				fields.push(vlq(src - state[0]), vlq(line - state[1]), vlq(c - state[2]));
				state[0] = src; state[1] = line; state[2] = c;
				if (rand(3) === 0) {
					const name = rand(50);
					fields.push(vlq(name - state[3]));
					state[3] = name;
				}
			}
			segs.push(fields.join(""));
		}
		lines.push(segs.join(","));
	}
	return JSON.stringify({
		version: 3,
		sources: ["src/a.ts", "src/b.ts", "node_modules/lib/index.js"],
		names: Array.from({ length: 50 }, (_, i) => `name${i}`),
		mappings: lines.join(";"),
		sourcesContent: ["export const a = 1;\n", null, "module.exports = {};\n"],
	});
}

const cases = {
	simple: '{"version":3,"file":"x.js","sources":["a.js"],"names":["foo"],"mappings":"AAAAA,CAAC;AACA"}',
	"source-root": '{"version":3,"sourceRoot":"src","sources":["a.ts","b.ts"],"mappings":"AAAA,ECAA;ADCA"}',
	"source-root-slash": '{"version":3,"sourceRoot":"webpack:///","sources":["./a.js"],"mappings":"AAAA"}',
	"null-source": '{"version":3,"sourceRoot":"r","sources":[null,"b.js"],"sourcesContent":[null,"x"],"mappings":"AAAA,ACAA"}',
	unicode: JSON.stringify({ version: 3, sources: ["é.js"], names: ["é", "🙂"], sourcesContent: ["const é = '🙂';\n"], mappings: "AAAAA,EAAEC" }),
	unsorted: '{"version":3,"sources":["a.js"],"mappings":"KAAA,HAAA,EAAE"}',
	"empty-lines": '{"version":3,"sources":["a.js"],"mappings":";;AAAA;;"}',
	"empty-mappings": '{"version":3,"sources":["a.js"],"mappings":""}',
	"one-field": '{"version":3,"sources":["a.js"],"mappings":"A,C,AAAA"}',
	"content-null": '{"version":3,"sources":["a.js"],"sourcesContent":null,"mappings":"AAAA"}',
	"version-float": '{"version":3.0,"sources":["a.js"],"mappings":"AAAA"}',
	big: bigMap(),
	// Invalid maps: the error message must be the same.
	"bad-sections": '{"version":3,"sections":[]}',
	"bad-version": '{"version":2,"sources":[],"mappings":""}',
	"bad-mappings": '{"version":3,"sources":[],"mappings":1}',
	"bad-sources": '{"version":3,"sources":[1],"mappings":""}',
	"bad-names": '{"version":3,"sources":[],"names":null,"mappings":""}',
	"bad-content": '{"version":3,"sources":["a"],"sourcesContent":[1],"mappings":""}',
	"bad-two-fields": '{"version":3,"sources":["a.js"],"mappings":"AA"}',
	"bad-six-fields": '{"version":3,"sources":["a.js"],"mappings":"AAAAAA"}',
	"bad-char": '{"version":3,"sources":["a.js"],"mappings":"A!AA"}',
	"bad-incomplete": '{"version":3,"sources":["a.js"],"mappings":"g"}',
	"bad-negative": '{"version":3,"sources":["a.js"],"mappings":"AAAD"}',
	"bad-source-index": '{"version":3,"sources":[],"mappings":"AAAA"}',
	"bad-name-index": '{"version":3,"sources":["a.js"],"names":[],"mappings":"AAAAA"}',
	"bad-vlq-large": '{"version":3,"sources":["a.js"],"mappings":"gggggggA"}',
	"bad-value-large": `{"version":3,"sources":["a.js"],"mappings":"${vlq(2 ** 31)}"}`,
	"bad-array": "[]",
	"bad-null": "null",
};

if (esbuild) {
	const dir = mkdtempSync(join(tmpdir(), "ce-esbuild-"));
	writeFileSync(join(dir, "app.ts"), [
		"interface User { id: number; name: string }",
		"export function loadUser(users: User[], id: number): string {",
		"  const user = users.find((u) => u.id === id);",
		"  return user!.name.toUpperCase();",
		"}",
		"console.log(loadUser([{ id: 1, name: 'é' }], 2));",
		"",
	].join("\n"));
	execFileSync(esbuild, [join(dir, "app.ts"), "--bundle", "--minify", "--sourcemap", `--outfile=${join(dir, "app.min.js")}`]);
	cases.esbuild = readFileSync(join(dir, "app.min.js.map"), "utf8");
}

for (const [name, text] of Object.entries(cases)) {
	writeFileSync(join(out, `${name}.map`), text);
	try {
		writeFileSync(join(out, `${name}.cksi`), buildSourceMapIndex(JSON.parse(text)));
	} catch (error) {
		writeFileSync(join(out, `${name}.error`), `${error.message}\n`);
	}
}
console.log(`Wrote ${Object.keys(cases).length} fixtures to ${out}`);
