import { readFileSync } from "node:fs";

// package.json is one directory up from both src/ and dist/.
export const VERSION: string = JSON.parse(
	readFileSync(new URL("../package.json", import.meta.url), "utf8"),
).version;
