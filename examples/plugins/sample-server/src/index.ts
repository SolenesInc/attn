import { AttnPluginClient, type DriverSpawnParams } from "@victorarias/attn-plugin";

const harnessId = "sample-server";
const executable = process.env.ATTN_SAMPLE_SERVER_EXECUTABLE || "sample-server";
const client = new AttnPluginClient({ version: "0.1.0" });

function view(launch: DriverSpawnParams) {
  if (!launch.conversation_id) throw new Error("server view needs conversation_id");
  const argv = [executable, "--attach", "--conversation", launch.conversation_id];
  if (launch.execution_target) argv.push("--target", launch.execution_target);
  if (launch.yolo) argv.push("--auto");
  if (launch.model) {
    const slash = launch.model.indexOf("/");
    if (slash >= 0) argv.push("--provider", launch.model.slice(0, slash));
    argv.push("--model", slash >= 0 ? launch.model.slice(slash + 1) : launch.model);
  }
  if (launch.effort) argv.push("--effort", launch.effort);
  if (launch.instructions) argv.push("--instructions", launch.instructions.content);
  if (launch.initial_prompt) argv.push("--", launch.initial_prompt);
  return { argv };
}

client.driver({
  id: harnessId,
  spawn: view,
  resume: view,
  yolo: true,
  initialPrompt: true,
  modelPin: true,
  effortPin: true,
  launchInstructions: true,
  server: {
    protocol: "attn-app-server-v1",
    executable,
    executableEnv: "ATTN_SAMPLE_SERVER_EXECUTABLE",
    serveArgs: ["serve"],
    homeEnv: "SAMPLE_SERVER_HOME",
    defaultHome: ".sample-server",
    socketName: "sample-server.sock",
    initialize: { server: "sample-server", protocol: { exact: 1 } },
    callerConversationEnv: "SAMPLE_SERVER_ROOT_CONVERSATION",
    onClose: "leave-running",
    recovery: "numbered-completions",
    targets: true,
  },
});
await client.connect();
