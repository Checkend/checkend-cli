// End-to-end test against a running Checkend server, for example `wrangler dev`.
// It runs only when CHECKEND_E2E_URL and CHECKEND_E2E_TOKEN are set. See README.md.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { Harness } from "../support/harness.ts";

const url = process.env.CHECKEND_E2E_URL;
const token = process.env.CHECKEND_E2E_TOKEN;

describe.skipIf(!url || !token)("against a real Checkend server", () => {
	it("logs in, links a repo, receives an error and shows it", async () => {
		const cli = new Harness();
		const login = await cli.run(["login", "--url", url ?? "", "--with-token"], {
			stdin: token ?? "",
		});
		expect(login.code, login.stderr).toBe(0);

		const status = await cli.run(["status"]);
		expect(status.code, status.stderr).toBe(0);
		expect(JSON.parse(status.stdout).health.status).toBe("ok");

		const name = `cli-e2e-${Date.now()}`;
		const init = await cli.run(["init", "--name", name, "--platform", "node"]);
		expect(init.code, init.stderr).toBe(0);
		const { dsn, project } = JSON.parse(init.stdout);
		expect(JSON.parse(readFileSync(join(cli.repo, ".checkend.json"), "utf8"))).toEqual({
			project: String(project.id),
			slug: project.slug,
		});
		expect(readFileSync(join(cli.repo, ".checkend.json"), "utf8")).not.toContain(token);

		const projectDsn = await cli.run(["project", "dsn"]);
		expect(JSON.parse(projectDsn.stdout).data.dsns).toContain(dsn);

		// Send one error, as a Sentry SDK does.
		const parsed = new URL(dsn);
		const store = `${url}/api${parsed.pathname}/store/`;
		const message = `e2e boom ${name}`;
		const response = await fetch(store, {
			method: "POST",
			headers: {
				"content-type": "application/json",
				"x-sentry-auth": `Sentry sentry_version=7, sentry_key=${parsed.username}, sentry_client=checkend-cli-e2e/1.0`,
			},
			body: JSON.stringify({
				event_id: crypto.randomUUID().replaceAll("-", ""),
				timestamp: Date.now() / 1000,
				level: "error",
				platform: "node",
				release: "e2e-1",
				environment: "test",
				exception: {
					values: [
						{
							type: "TypeError",
							value: message,
							stacktrace: {
								frames: [
									{ filename: "src/e2e.ts", function: "runE2e", lineno: 7, colno: 3, in_app: true },
								],
							},
						},
					],
				},
				tags: { suite: "cli-e2e" },
			}),
		});
		expect(response.status, await response.clone().text()).toBe(200);

		const next = await cli.run(["next"]);
		expect(next.code, next.stderr).toBe(0);
		const issue = JSON.parse(next.stdout).data;
		expect(issue.title).toContain(message);

		const list = await cli.run(["issue", "list", "--status", "unresolved"]);
		expect(JSON.parse(list.stdout).data.map((i: { id: number }) => i.id)).toContain(issue.id);

		const view = await cli.run(["issue", "view", `${project.slug}-${issue.id}`], { tty: true });
		expect(view.code, view.stderr).toBe(0);
		expect(view.stdout).toContain(message);

		const event = await cli.run(["event", "view", String(issue.latest_event_id)], { tty: true });
		expect(event.code, event.stderr).toBe(0);
		expect(event.stdout).toContain("runE2e (src/e2e.ts:7:3)");
		expect(event.stdout).toMatch(/suite\s+cli-e2e/);

		// An unknown command does nothing, even with a real server.
		const typo = await cli.run(["isue", "list"]);
		expect(typo.code).toBe(2);

		// The token never appears in any output.
		for (const result of [login, status, init, projectDsn, next, list, view, event]) {
			expect(result.all).not.toContain(token);
		}
	});
});
