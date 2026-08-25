// The scaffold's tsconfig sets `jsxImportSource` to the SDK, so `<Thing />` in a view imports this module rather than React, putting an app's elements on attn's own React instance.

export { Fragment, jsx, jsxs } from "react/jsx-runtime"
export type { JSX } from "react/jsx-runtime"
