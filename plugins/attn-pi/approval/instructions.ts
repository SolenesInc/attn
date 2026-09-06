// The two texts the model is given when the Guardian refuses or runs out of time.
// Verbatim from codex-rs/core/src/guardian/review.rs:64-82.

export const guardianRejectionInstructions =
  "The agent must not attempt to achieve the same outcome via workaround, " +
  "indirect execution, or policy circumvention. " +
  "Proceed only with a materially safer alternative, " +
  "or if the user explicitly approves the action after being informed of the risk. " +
  "Otherwise, stop and request user input.";

export const guardianTimeoutInstructions =
  "The automatic permission approval review did not finish before its deadline. " +
  "Do not assume the action is unsafe based on the timeout alone. " +
  "You may retry once, or ask the user for guidance or explicit approval.";
