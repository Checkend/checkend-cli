// Tests the npm wrapper with a fake release on a local server and a fake binary (a shell script).
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { cpSync, existsSync, mkdtempSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { after, before, test } from "node:test";
import { fileURLToPath } from "node:url";
import { assetName } from "../lib/binary.js";
import { ALLOWED_SIGNERS } from "../lib/keys.js";
import { NAMESPACE, verifySignature } from "../lib/sshsig.js";

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

// The TEST release key of these tests only: the published package trusts no key until the first
// release key exists.
const testKey = generateKeyPairSync("ed25519");
const testLine = authorizedKey(testKey.publicKey);

function sshString(buf) {
	const len = Buffer.alloc(4);
	len.writeUInt32BE(buf.length);
	return Buffer.concat([len, buf]);
}

function authorizedKey(publicKey) {
	const raw = Buffer.from(publicKey.export({ format: "jwk" }).x, "base64url");
	return `ssh-ed25519 ${Buffer.concat([sshString(Buffer.from("ssh-ed25519")), sshString(raw)]).toString("base64")}`;
}

/** An SSH signature (ssh-keygen -Y sign) of message, made with key in namespace. */
function sshSign(key, message, namespace = NAMESPACE) {
	const digest = createHash("sha512").update(message).digest();
	const signed = Buffer.concat([Buffer.from("SSHSIG"), sshString(Buffer.from(namespace)), sshString(Buffer.alloc(0)), sshString(Buffer.from("sha512")), sshString(digest)]);
	const sig = sign(null, signed, key.privateKey);
	const pub = Buffer.from(authorizedKey(key.publicKey).split(" ")[1], "base64");
	const version = Buffer.alloc(4);
	version.writeUInt32BE(1);
	const blob = Buffer.concat([Buffer.from("SSHSIG"), version, sshString(pub), sshString(Buffer.from(namespace)), sshString(Buffer.alloc(0)), sshString(Buffer.from("sha512")), sshString(Buffer.concat([sshString(Buffer.from("ssh-ed25519")), sshString(sig)]))]);
	return `-----BEGIN SSH SIGNATURE-----\n${blob.toString("base64").match(/.{1,70}/g).join("\n")}\n-----END SSH SIGNATURE-----\n`;
}

/**
 * A copy of the package for version 1.2.3 with this checksum line. sig: "valid", "missing",
 * "other-key", "namespace" or "changed"; trust: the key lines of lib/keys.js in the copy.
 */
function packageCopy(checksum, { sig = "valid", trust = [testLine] } = {}) {
	const dir = mkdtempSync(join(tmpdir(), "ce-npm-"));
	// Not the tests, and not the checksums of a real release: the release job puts them into npm/
	// before it runs the tests, and each test writes its own.
	const own = new Set([join(source, "test"), join(source, "checksums.txt"), join(source, "checksums.txt.sig")]);
	cpSync(source, dir, { recursive: true, filter: (p) => ![...own].some((o) => p === o || p.startsWith(`${o}/`)) });
	const pkg = join(dir, "package.json");
	writeFileSync(pkg, JSON.stringify({ name: "@checkend/cli", version: "1.2.3", type: "module" }));
	const keys = trust.map((key, i) => `\t{ id: "test-${i}", key: ${JSON.stringify(key)} }, // TEST KEY\n`).join("");
	writeFileSync(join(dir, "lib", "keys.js"), `export const ALLOWED_SIGNERS = [\n${keys}];\n`);
	if (checksum === null) return dir;
	let text = `${checksum}  ${assetName()}\n`;
	const signature = {
		valid: () => sshSign(testKey, text),
		"other-key": () => sshSign(generateKeyPairSync("ed25519"), text),
		namespace: () => sshSign(testKey, text, "checkend-server-release"),
		changed: () => sshSign(testKey, text),
		missing: () => null,
	}[sig]();
	if (sig === "changed") text += `${"2".repeat(64)}  extra\n`;
	writeFileSync(join(dir, "checksums.txt"), text);
	if (signature !== null) writeFileSync(join(dir, "checksums.txt.sig"), signature);
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

test("refuses a missing, wrong or changed signature, or an unknown key, and keeps nothing", { skip }, async () => {
	for (const [sig, message] of [
		["missing", /no signature/],
		["other-key", /unknown key SHA256:/],
		["namespace", /namespace/],
		["changed", /not valid/],
	]) {
		const dir = packageCopy(sha(fake), { sig });
		const cache = mkdtempSync(join(tmpdir(), "ce-cache-"));
		const before = requests;
		const result = await run(dir, cache, ["--version"]);
		assert.equal(result.status, 1, sig);
		assert.match(result.stderr, message, sig);
		assert.equal(result.stdout, "");
		assert.equal(requests, before, `${sig}: nothing is downloaded`);
		assert.equal(existsSync(join(cache, "1.2.3")), false);
	}
});

test("the published package has only real keys, and without a key it fails closed", { skip }, async () => {
	for (const { id, key } of ALLOWED_SIGNERS) {
		assert.match(key, /^ssh-ed25519 [A-Za-z0-9+/]+=*$/);
		assert.doesNotMatch(`${id} ${key}`, /test/i, "no test key in the published package");
	}
	const dir = packageCopy(sha(fake), { trust: [] });
	const result = await run(dir, mkdtempSync(join(tmpdir(), "ce-cache-")), ["--version"]);
	assert.equal(result.status, 1);
	assert.match(result.stderr, /trusts no release key yet/);
});

test("refuses bytes after the last field of a signature", () => {
	const text = "abc\n";
	const good = sshSign(testKey, text);
	const allowed = [{ id: "t", key: testLine }];
	assert.equal(verifySignature(good, text, NAMESPACE, allowed), "t");
	const blob = Buffer.from(good.split("\n").slice(1, -2).join(""), "base64");
	const armored = (b) => `-----BEGIN SSH SIGNATURE-----\n${b.toString("base64")}\n-----END SSH SIGNATURE-----\n`;
	assert.throws(() => verifySignature(armored(Buffer.concat([blob, Buffer.from([0])])), text, NAMESPACE, allowed), /not an SSH signature/);
	assert.throws(() => verifySignature("garbage", text, NAMESPACE, allowed), /not an SSH signature/);
});

test("verifies a signature of ssh-keygen", { skip: skip || !hasSshKeygen() }, () => {
	const dir = mkdtempSync(join(tmpdir(), "ce-keygen-"));
	execFileSync("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", join(dir, "key")]);
	writeFileSync(join(dir, "checksums.txt"), "abc  ce_darwin_arm64\n");
	execFileSync("ssh-keygen", ["-Y", "sign", "-f", join(dir, "key"), "-n", NAMESPACE, join(dir, "checksums.txt")], { stdio: "ignore" });
	const key = readFileSync(join(dir, "key.pub"), "utf8").split(" ").slice(0, 2).join(" ");
	const id = verifySignature(readFileSync(join(dir, "checksums.txt.sig"), "utf8"), readFileSync(join(dir, "checksums.txt")), NAMESPACE, [{ id: "k", key }]);
	assert.equal(id, "k");
});

function hasSshKeygen() {
	try {
		execFileSync("ssh-keygen", ["-Y", "verify"], { stdio: "ignore" });
	} catch (error) {
		return error.status !== undefined && error.status !== null && error.code !== "ENOENT";
	}
	return true;
}
