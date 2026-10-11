// Tests the npm wrapper with a fake release on a local server and a fake binary (a shell script).
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { cpSync, existsSync, mkdtempSync, readdirSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { after, before, test } from "node:test";
import { fileURLToPath } from "node:url";
import { assetName } from "../lib/binary.js";

const source = join(dirname(fileURLToPath(import.meta.url)), "..");
const skip = process.platform === "win32" ? "the fake binary is a shell script" : false;
const fake = "#!/bin/sh\necho \"fake-ce $*\"\nexit 3\n";
const sha = (text) => createHash("sha256").update(text).digest("hex");

let server;
let base;
let requests = 0;
before(async () => {
	server = createServer((req, res) => {
		res.setHeader("Connection", "close"); // no keep-alive socket keeps the server open
		requests++;
		if (req.url === `/download/v1.2.3/${assetName()}`) return res.end(fake);
		res.statusCode = 404;
		res.end();
	});
	await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
	base = `http://127.0.0.1:${server.address().port}`;
});
after(() => server.close());

function packageCopy(checksum) {
	const dir = mkdtempSync(join(tmpdir(), "ce-npm-"));
	cpSync(source, dir, { recursive: true, filter: (p) => !p.includes(`${join(source, "test")}`) });
	const pkg = join(dir, "package.json");
	writeFileSync(pkg, JSON.stringify({ name: "@checkend/cli", version: "1.2.3", type: "module" }));
	if (checksum !== null) writeFileSync(join(dir, "checksums.txt"), `${checksum}  ${assetName()}\n`);
	return dir;
}

// An async spawn: the fake server runs in this process, so it must not be blocked.
function run(dir, cache, args) {
	return new Promise((resolve) => {
		const child = spawn(process.execPath, [join(dir, "bin", "ce.js"), ...args], {
			env: { PATH: process.env.PATH, CE_DOWNLOAD_BASE: base, CHECKEND_CLI_CACHE: cache },
		});
		let stdout = "";
		let stderr = "";
		child.stdout.on("data", (d) => (stdout += d));
		child.stderr.on("data", (d) => (stderr += d));
		child.on("close", (status) => resolve({ status, stdout, stderr }));
	});
}

test("downloads, checks and runs the binary with the same arguments and exit code", { skip }, async () => {
	const dir = packageCopy(sha(fake));
	const cache = mkdtempSync(join(tmpdir(), "ce-cache-"));
	const first = await run(dir, cache, ["issue", "list"]);
	assert.equal(first.stdout, "fake-ce issue list\n");
	assert.equal(first.status, 3);
	assert.match(first.stderr, /Downloading ce 1\.2\.3/);
	const before = requests;
	const second = await run(dir, cache, ["--version"]);
	assert.equal(second.stdout, "fake-ce --version\n");
	assert.equal(second.stderr, "");
	assert.equal(requests, before, "the second run downloads nothing");
});

test("refuses a binary with a wrong SHA-256 and keeps nothing", { skip }, async () => {
	const dir = packageCopy("0".repeat(64));
	const cache = mkdtempSync(join(tmpdir(), "ce-cache-"));
	const result = await run(dir, cache, ["--version"]);
	assert.equal(result.status, 1);
	assert.match(result.stderr, /SHA-256 of .* is wrong/);
	assert.equal(result.stdout, "");
	assert.deepEqual(readdirSync(cache).flatMap((v) => readdirSync(join(cache, v))), []);
});

test("refuses to download without checksums.txt", { skip }, async () => {
	const dir = packageCopy(null);
	const cache = mkdtempSync(join(tmpdir(), "ce-cache-"));
	const result = await run(dir, cache, ["--version"]);
	assert.equal(result.status, 1);
	assert.match(result.stderr, /no checksums\.txt/);
	assert.equal(existsSync(join(cache, "1.2.3")), false);
});

test("names the release file of each platform", () => {
	assert.equal(assetName("darwin", "arm64"), "ce_darwin_arm64");
	assert.equal(assetName("linux", "x64"), "ce_linux_amd64");
	assert.equal(assetName("win32", "x64"), "ce_windows_amd64.exe");
	assert.throws(() => assetName("aix", "ppc64"), /no binary/);
});
