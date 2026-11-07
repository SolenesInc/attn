import { eslintCompatPlugin } from "@oxlint/plugins";

import { noDocumentBodyPortalRule } from "./rules/no-document-body-portal.ts";
import { noDomInstanceofRule } from "./rules/no-dom-instanceof.ts";
import { noMockingAttnModulesRule } from "./rules/no-mocking-attn-modules.ts";
import { noRealTimeWaitsRule } from "./rules/no-real-time-waits.ts";

const attnPlugin = eslintCompatPlugin({
  meta: { name: "attn" },
  rules: {
    "no-document-body-portal": noDocumentBodyPortalRule,
    "no-dom-instanceof": noDomInstanceofRule,
    "no-mocking-attn-modules": noMockingAttnModulesRule,
    "no-real-time-waits": noRealTimeWaitsRule,
  },
});

export default attnPlugin;
