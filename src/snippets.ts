// Platform detection and the SDK setup snippet that `ce init` prints. Checkend accepts
// events from any Sentry SDK.
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { CliError } from "./errors.ts";

const FRONTEND = ["react", "react-dom", "vue", "svelte", "@angular/core", "preact", "solid-js"];
const SERVER = [
	"express",
	"fastify",
	"koa",
	"hono",
	"@nestjs/core",
	"next",
	"nuxt",
	"@remix-run/node",
];

/** Guesses the platform from the files in the repo root. */
export function detectPlatform(root: string): string | undefined {
	const has = (file: string) => existsSync(join(root, file));
	if (has("Gemfile")) return "ruby";
	if (has("pyproject.toml") || has("requirements.txt") || has("setup.py")) return "python";
	if (has("go.mod")) return "go";
	if (has("composer.json")) return "php";
	if (has("package.json")) {
		let deps: Record<string, unknown> = {};
		try {
			const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
			deps = { ...pkg.dependencies, ...pkg.devDependencies };
		} catch {
			// An unreadable package.json is still a JavaScript project.
		}
		const frontend = FRONTEND.some((name) => name in deps);
		const server = SERVER.some((name) => name in deps);
		return frontend && !server ? "javascript" : "node";
	}
	return undefined;
}

/**
 * A DSN goes into code that people paste, so it must be a plain URL: no quotes, spaces or
 * backslashes.
 */
export function checkDsn(dsn: unknown): string {
	if (typeof dsn === "string" && /^https?:\/\/[^\s"'`\\<>]+$/.test(dsn) && URL.canParse(dsn)) {
		return dsn;
	}
	throw new CliError("The server returned a DSN that is not a valid URL.");
}

export function snippet(platform: string | undefined, dsn: string): string {
	switch (platform) {
		case "node":
		case "javascript": {
			const pkg = platform === "node" ? "@sentry/node" : "@sentry/browser";
			return `// npm install ${pkg}\nimport * as Sentry from "${pkg}";\n\nSentry.init({ dsn: "${dsn}" });`;
		}
		case "python":
			return `# pip install sentry-sdk\nimport sentry_sdk\n\nsentry_sdk.init(dsn="${dsn}")`;
		case "ruby":
			return `# Gemfile: gem "sentry-ruby"\nSentry.init do |config|\n  config.dsn = "${dsn}"\nend`;
		case "go":
			return `// go get github.com/getsentry/sentry-go\nsentry.Init(sentry.ClientOptions{Dsn: "${dsn}"})`;
		case "php":
			return `// composer require sentry/sentry\n\\Sentry\\init(['dsn' => '${dsn}']);`;
		default:
			return `// Use the Sentry SDK for your platform with this DSN:\nSentry.init({ dsn: "${dsn}" });`;
	}
}
