import { appendFileSync, existsSync, mkdirSync } from "node:fs";
import { join, resolve } from "node:path";

export const RECEIPTS_DIR = resolve(import.meta.dirname);
export const LOGS_DIR = join(RECEIPTS_DIR, "logs");

if (!existsSync(LOGS_DIR)) mkdirSync(LOGS_DIR, { recursive: true });

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
