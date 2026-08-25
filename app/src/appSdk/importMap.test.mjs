import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"


const read = (path) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8")

const sdkPackage = JSON.parse(read("../../../sdk/attn-app/package.json"))
const indexHtml = read("../../index.html")
const viteConfig = read("../../vite.config.ts")
const appbuildCodegen = read("../../../internal/appbuild/codegen.go")

const importMap = JSON.parse(
  indexHtml.match(/<script type="importmap">([\s\S]*?)<\/script>/)[1],
).imports

const sdkSpecifiers = Object.keys(sdkPackage.exports).map((key) =>
  key === "." ? sdkPackage.name : sdkPackage.name + key.slice(1),
)

describe("the app SDK's import map", () => {
  it("resolves every specifier the SDK package exports", () => {
    expect(Object.keys(importMap).sort()).toEqual(sdkSpecifiers.sort())
  })

  it("resolves every specifier the view build marks external", () => {
    const goList = appbuildCodegen.match(/func SDKSpecifiers\(\) \[\]string \{[\s\S]*?\n\}/)[0]
    const suffixes = [...goList.matchAll(/SDKModule \+ "([^"]+)"/g)].map((m) => m[1])
    const external = [sdkPackage.name, ...suffixes.map((s) => sdkPackage.name + s)]
    expect(external.sort()).toEqual(sdkSpecifiers.sort())
  })

  it("points every specifier at a chunk the build emits under a fixed name", () => {
    const chunks = viteConfig.match(/const APP_SDK_CHUNKS[\s\S]*?\n\};/)[0]
    const names = [...chunks.matchAll(/"([a-z0-9-]+)":/g)].map((m) => m[1])
    expect(Object.values(importMap).sort()).toEqual(names.map((n) => `/${n}.js`).sort())
  })
})
