import { definePlugin } from "@oxlint/plugins";

import { commentBlockMaxLinesRule } from "./rules/comment-block-max-lines.ts";

export default definePlugin({
  meta: { name: "attn" },
  rules: { "comment-block-max-lines": commentBlockMaxLinesRule },
});
