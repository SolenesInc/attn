import { describe, expect, it } from "vitest"
import * as React from "react"
import * as sdk from "@victorarias/attn-app"
import * as sdkJsx from "@victorarias/attn-app/jsx-runtime"
import * as reactJsx from "react/jsx-runtime"

// Two React instances share no hook dispatcher, so the second one's useState throws on
// first render. A dependency bump can silently split them.
describe("the app SDK's React", () => {
  it("is the same module instance the frontend uses", () => {
    expect(sdk.useState).toBe(React.useState)
    expect(sdk.useEffect).toBe(React.useEffect)
    expect(sdk.useMemo).toBe(React.useMemo)
    expect(sdk.useCallback).toBe(React.useCallback)
    expect(sdk.useRef).toBe(React.useRef)
    expect(sdk.useReducer).toBe(React.useReducer)
    expect(sdk.Fragment).toBe(React.Fragment)
  })

  it("hands a view the same JSX runtime attn's own components compile against", () => {
    expect(sdkJsx.jsx).toBe(reactJsx.jsx)
    expect(sdkJsx.jsxs).toBe(reactJsx.jsxs)
  })

  // `export *` would have made React's whole surface the SDK's promise.
  it("re-exports React by name, not wholesale", () => {
    const reactNames = Object.keys(sdk).filter((name) => name in React)
    expect(reactNames.sort()).toEqual([
      "Fragment",
      "useCallback",
      "useEffect",
      "useMemo",
      "useReducer",
      "useRef",
      "useState",
    ])
  })
})
