#!/usr/bin/env node
// `ce` and `checkend` from npm: runs the ce binary with the same arguments, stdio and exit code.
import { spawnSync } from "node:child_process";
import { binaryPath } from "../lib/binary.js";

let file;
try {
	file = await binaryPath();
} catch (error) {
	process.stderr.write(`ce: ${error.message}\n`);
	process.exit(1);
}
const result = spawnSync(file, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
	process.stderr.write(`ce: cannot run ${file}: ${result.error.message}\n`);
	process.exit(1);
}
if (result.signal) process.kill(process.pid, result.signal);
process.exit(result.status ?? 1);
