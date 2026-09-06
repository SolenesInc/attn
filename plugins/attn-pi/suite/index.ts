// pi re-runs this default export on every session transition, so the suite lives in a
// process-wide slot — module scope is NOT one, see ./singleton.
import { VERSION, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { PiSecurity } from "../security/index";
import { denialLedgerFor } from "../automode/ledger";
import { attnApprovalSource, PiApproval, proxyFromEnvironment } from "../approval/index";
import { AttnPiSuite, type ExtensionAPILike } from "./core";
import { processSingleton } from "./singleton";

const suite = processSingleton(
  "attn:pi-suite",
  () =>
    new AttnPiSuite({
      socketPath: process.env.ATTN_PI_SUITE_SOCKET,
      token: process.env.ATTN_PI_TOKEN,
      piVersion: VERSION,
      proxyCredentials: process.env.ATTN_PI_PROXY_CREDENTIALS,
    }),
);

const approval = processSingleton("attn:pi-approval", () => {
  const source = attnApprovalSource(process.env);
  if (!source) return undefined;
  const proxy = proxyFromEnvironment(process.env);
  return new PiApproval({
    config: source.config,
    suite,
    ledger: denialLedgerFor(process.env),
    ...(proxy ? { proxy } : {}),
    ...(source.problem ? { notice: source.problem } : {}),
  });
});

const security = processSingleton(
  "attn:pi-security",
  () =>
    // The security policy contributes only paths; the daemon's approval config
    // owns the sandbox mode and the network switch.
    new PiSecurity(undefined, approval?.runBash, (policy) => approval?.useSandbox(policy)),
);

export default function attnPiSuite(pi: ExtensionAPILike & ExtensionAPI): void {
  if (approval) security.register(pi);
  suite.register(pi);
  approval?.register(pi);
}
