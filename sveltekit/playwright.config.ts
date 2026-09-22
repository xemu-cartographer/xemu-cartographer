import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
	testDir: 'e2e',
	fullyParallel: true,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 2 : 0,
	workers: process.env.CI ? 1 : undefined,
	reporter: 'html',
	use: {
		baseURL: 'http://localhost:4173',
		trace: 'on-first-retry'
	},
	projects: [
		{
			name: 'chromium',
			use: { ...devices['Desktop Chrome'] }
		}
	],
	webServer: {
		// Run sirv's bin directly, not through `pnpm exec`. Playwright stops the
		// server by SIGKILLing its process group and then waits for the process's
		// stdio pipes to close. pnpm >= 11.27.1 starts the exec'd command in a
		// session of its own (so its new signal forwarding can own Ctrl+C), so the
		// kill takes sh + pnpm but leaves sirv running with the pipes open, and
		// `playwright test` never exits after the last test passes (CI,
		// 2026-09-21, run 35657730454 — the 6 h "hang"; reproduced locally with
		// `npx pnpm@11.27.1 test:e2e`: 15 passed, then no exit).
		command: 'node node_modules/sirv-cli/bin.js ../pb_public --single --port 4173 --quiet',
		port: 4173,
		reuseExistingServer: !process.env.CI
	}
});
