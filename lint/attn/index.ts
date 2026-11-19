import { eslintCompatPlugin } from "@oxlint/plugins";

import { noMockingAttnModulesRule } from "./rules/no-mocking-attn-modules.ts";
import { noRealTimeWaitsRule } from "./rules/no-real-time-waits.ts";

const attnPlugin = eslintCompatPlugin({
  meta: { name: "attn" },
  rules: {
    "no-mocking-attn-modules": noMockingAttnModulesRule,
    "no-real-time-waits": noRealTimeWaitsRule,
  },
});

export default attnPlugin;
