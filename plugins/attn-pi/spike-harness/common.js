import { appendFileSync, existsSync, mkdirSync, readdirSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import {
	createAgentSession,
	DefaultResourceLoader,
	SessionManager,
	SettingsManager,
} from "@earendil-works/pi-coding-agent";
// getModel is not exported from the package root in 0.80.10, only from the
// /compat subpath (deprecated there but functional).
import { getModel } from "@earendil-works/pi-ai/compat";

export const SPIKE_DIR = resolve(import.meta.dirname);
export const LOGS_DIR = join(SPIKE_DIR, "logs");
export const SESSIONS_DIR = join(SPIKE_DIR, "sessions");
// The real ~/.pi/agent, for auth/resource resolution only. Never written to;
// session storage is redirected separately in buildSession.
export const REAL_AGENT_DIR = join(homedir(), ".pi", "agent");
export const REAL_SESSIONS_DIR = join(REAL_AGENT_DIR, "sessions");

if (!existsSync(LOGS_DIR)) mkdirSync(LOGS_DIR, { recursive: true });
if (!existsSync(SESSIONS_DIR)) mkdirSync(SESSIONS_DIR, { recursive: true });

// Cheapest openai model per `pi --list-models openai` and the provider cost
// table: gpt-5.6-luna at $1/$6 per M input/output.
export const MODEL_PROVIDER = "openai";
export const MODEL_ID = "gpt-5.6-luna";
export const CHEAP_MODEL = () => getModel(MODEL_PROVIDER, MODEL_ID);

export function createLogger(scenario) {
	const path = join(LOGS_DIR, `${scenario}.jsonl`);
	return {
		path,
		log(surface, type, extra = {}) {
			const rec = { t: performance.now(), scenario, surface, type, bytes: 0, ...extra };
			rec.bytes = Buffer.byteLength(JSON.stringify(rec));
			appendFileSync(path, `${JSON.stringify(rec)}\n`);
			return rec;
		},
	};
}

export async function buildSession(scenario, { extensionFactory } = {}) {
	const cwd = SPIKE_DIR;
	const sessionDir = join(SESSIONS_DIR, scenario);
	if (!existsSync(sessionDir)) mkdirSync(sessionDir, { recursive: true });

	const sessionManager = SessionManager.create(cwd, sessionDir);
	const settingsManager = SettingsManager.create(cwd);

	const resourceLoader = new DefaultResourceLoader({
		cwd,
		agentDir: REAL_AGENT_DIR,
		settingsManager,
		extensionFactories: extensionFactory ? [extensionFactory] : undefined,
	});
	await resourceLoader.reload();

	const { session, extensionsResult, modelFallbackMessage } = await createAgentSession({
		cwd,
		model: CHEAP_MODEL(),
		sessionManager,
		settingsManager,
		resourceLoader,
	});

	return { session, sessionManager, extensionsResult, modelFallbackMessage, sessionDir };
}

export async function openSession(sessionFilePath) {
	const cwd = SPIKE_DIR;
	const sessionManager = SessionManager.open(sessionFilePath);
	const settingsManager = SettingsManager.create(cwd);

	const resourceLoader = new DefaultResourceLoader({ cwd, agentDir: REAL_AGENT_DIR, settingsManager });
	await resourceLoader.reload();

	const { session, modelFallbackMessage } = await createAgentSession({
		cwd,
		model: CHEAP_MODEL(),
		sessionManager,
		settingsManager,
		resourceLoader,
	});

	return { session, sessionManager, modelFallbackMessage };
}

export function sleep(ms) {
	return new Promise((r) => setTimeout(r, ms));
}

export function countFiles(dir) {
	try {
		return readdirSync(dir).length;
	} catch {
		return 0;
	}
}
