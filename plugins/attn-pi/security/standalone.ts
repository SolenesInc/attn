import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { processSingleton } from "../suite/singleton";
import { PiSecurity } from "./index";

const security = processSingleton("attn:pi-security", () => new PiSecurity());

export default function attnSecurity(pi: ExtensionAPI): void {
  security.register(pi);
}
