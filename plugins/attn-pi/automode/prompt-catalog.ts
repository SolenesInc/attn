import catalog from "./prompts.generated.json";
import { renderCatalog } from "../../../internal/prompts/renderer";
export function renderPrompt(
  event: string,
  values: Record<string, string>,
  recipient = "pi-permission",
): string {
  return renderCatalog(catalog, recipient, event, values);
}
