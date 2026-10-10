import { defineConfig } from "vitest/config";

// End-to-end tests against a running Checkend server. See README.md, "End-to-end test".
export default defineConfig({
	test: {
		environment: "node",
		include: ["test/e2e/**/*.test.ts"],
		testTimeout: 30_000,
	},
});
