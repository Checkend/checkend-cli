// Finds or downloads the ce binary of this package version. The SHA-256 of every binary is in
// checksums.txt in this package (written at release time). Its SSH signature (checksums.txt.sig,
// a trusted key of keys.js) is checked first, then the SHA-256 of the download: a changed
// checksums.txt or a changed download does not run.
import { createHash } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { ALLOWED_SIGNERS } from "./keys.js";
import { NAMESPACE, verifySignature } from "./sshsig.js";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));

const OS = { darwin: "darwin", linux: "linux", win32: "windows" };
const ARCH = { x64: "amd64", arm64: "arm64" };

/** The release file of this platform, for example ce_darwin_arm64 or ce_windows_amd64.exe. */
export function assetName(platform = process.platform, arch = process.arch) {
	const os = OS[platform];
	const cpu = ARCH[arch];
	if (!os || !cpu) throw new Error(`ce has no binary for ${platform}/${arch}.`);
	return `ce_${os}_${cpu}${os === "windows" ? ".exe" : ""}`;
}

function cacheDir(env) {
	if (env.CHECKEND_CLI_CACHE) return env.CHECKEND_CLI_CACHE;
	if (process.platform === "win32") return join(env.LOCALAPPDATA ?? join(homedir(), "AppData", "Local"), "checkend", "cli");
	if (process.platform === "darwin") return join(homedir(), "Library", "Caches", "checkend", "cli");
	return join(env.XDG_CACHE_HOME ?? join(homedir(), ".cache"), "checkend", "cli");
}

function expectedSha(asset) {
	const file = join(root, "checksums.txt");
	if (!existsSync(file)) throw new Error("This package has no checksums.txt, so it cannot check the ce binary.");
	if (ALLOWED_SIGNERS.length === 0) {
		throw new Error("This package trusts no release key yet, so it installs nothing. Use: go install github.com/Checkend/checkend-cli/cmd/ce@latest");
	}
	const sig = join(root, "checksums.txt.sig");
	if (!existsSync(sig)) throw new Error("This package has no signature (checksums.txt.sig), so it installs nothing.");
	const text = readFileSync(file);
	verifySignature(readFileSync(sig, "utf8"), text, NAMESPACE, ALLOWED_SIGNERS);
	for (const line of text.toString("utf8").split("\n")) {
		const [sha, name] = line.trim().split(/\s+/);
		if (name === asset && /^[0-9a-f]{64}$/.test(sha ?? "")) return sha;
	}
	throw new Error(`checksums.txt has no line for ${asset}.`);
}

/** Returns the path of the binary, and downloads it the first time. */
export async function binaryPath(env = process.env) {
	const asset = assetName();
	const version = pkg.version;
	const file = join(cacheDir(env), version, asset);
	if (existsSync(file)) return file;

	const sha = expectedSha(asset);
	const base = env.CE_DOWNLOAD_BASE ?? "https://github.com/Checkend/checkend-cli/releases";
	const url = `${base}/download/v${version}/${asset}`;
	process.stderr.write(`Downloading ce ${version} (${asset})...\n`);
	const response = await fetch(url);
	if (!response.ok) throw new Error(`Cannot download ${url}: HTTP ${response.status}.`);
	const bytes = Buffer.from(await response.arrayBuffer());
	const actual = createHash("sha256").update(bytes).digest("hex");
	if (actual !== sha) throw new Error(`The SHA-256 of ${asset} is wrong, so it was not saved or run.`);

	mkdirSync(dirname(file), { recursive: true });
	const temp = `${file}.${process.pid}.tmp`;
	try {
		writeFileSync(temp, bytes, { mode: 0o755 });
		chmodSync(temp, 0o755);
		renameSync(temp, file);
	} finally {
		rmSync(temp, { force: true });
	}
	return file;
}
