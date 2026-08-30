// pi re-runs this default export on every session transition, so the suite lives in a
// process-wide slot — module scope is NOT one, see ./singleton.
import { VERSION } from "@earendil-works/pi-coding-agent";
import { AutoMode, type AutoModePiLike } from "../automode/mode";
import { denialLedgerFor } from "../automode/ledger";
import { attnAutoModeSource } from "../automode/source";
import { AttnPiSuite, type ExtensionAPILike } from "./core";
import { processSingleton } from "./singleton";

const suite = processSingleton(
  "attn:pi-suite",
  () =>
    new AttnPiSuite({
      socketPath: process.env.ATTN_PI_SUITE_SOCKET,
      token: process.env.ATTN_PI_TOKEN,
      piVersion: VERSION,
    }),
);

const autoMode = processSingleton("attn:pi-automode", () => {
  const source = attnAutoModeSource(process.env);
  return source
    ? new AutoMode({
        config: source.config,
        notice: source.problem,
        ...(process.env.ATTN_SESSION_ID ? { sessionKey: process.env.ATTN_SESSION_ID } : {}),
        ledger: denialLedgerFor(process.env),
        onDenial: (denial) => suite.reportDenial(denial),
        onWaitingForUser: (waiting) => suite.reportApprovalWindow(waiting),
      })
    : undefined;
});

export default function attnPiSuite(pi: ExtensionAPILike & AutoModePiLike): void {
  suite.register(pi);
  autoMode?.register(pi);
}
