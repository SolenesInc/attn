// Must not install signal handlers: the outer runner kill -9's this process
// mid-flight and needs it to die uncooperatively.
import { buildSession, createLogger } from "./common.js";

const SCENARIO = process.argv[2] ?? "crash-revive";
const logger = createLogger(`${SCENARIO}-child`);

async function main() {
	const { session } = await buildSession(SCENARIO);
	await session.bindExtensions({ mode: "print" });

	console.log(`SESSION_FILE:${session.sessionFile}`);
	console.log(`CHILD_PID:${process.pid}`);

	session.subscribe((event) => {
		logger.log("sdk", event.type, {
			note: event.type === "tool_execution_start" ? `toolName=${event.toolName}` : undefined,
		});
		if (event.type === "tool_execution_start") {
			console.log("TOOL_EXECUTION_START");
		}
	});

	console.log("PROMPT_CALLED");
	await session.prompt("Run the bash command `sleep 15 && echo done`, then summarize.");
	console.log("CHILD_PROMPT_SETTLED");
}

main().catch((err) => {
	logger.log("harness", "error", { note: String(err?.stack ?? err) });
	console.error(err);
	process.exit(1);
});
