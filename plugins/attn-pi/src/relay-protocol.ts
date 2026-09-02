
// `dropped_reports` counts reports the suite could not hand over since its last hello; it
// cannot log. `pi_state` is taken only when attn has nothing, or reconnects reopen turns.
export type RelayHelloParams = {
  token: string;
  pi_session_id: string;
  pi_version: string;
  reason: string;
  dropped_reports?: number;
  pi_state?: RelayHelloState;
};
export type RelayHelloState = "idle" | "working" | "pending_approval";
export type RelayHelloResult = { ok: true };
export type RelaySuiteState = "working" | "pending_approval";
export type RelayReportStateParams = { token: string; state: RelaySuiteState };
export type RelayReportInputTakenParams = { token: string; input_id: string };
export type RelayReportStopParams = { token: string; assistant_text: string; aborted?: boolean };
export type RelayReportPullRequestParams = { token: string; url: string };
export type RelayReportDenialParams = {
  token: string;
  tool: string;
  action: string;
  reason: string;
  rule: string;
  at: string;
};

export type RelayDeliverMessageParams = { input_id: string; text: string };
export type RelayDeliverMessageResult = { delivered: boolean };

export const relayMethods = {
  hello: "suite.hello",
  reportState: "suite.report_state",
  reportStop: "suite.report_stop",
  reportDenial: "suite.report_denial",
  reportInputTaken: "suite.report_input_taken",
  reportPullRequest: "suite.report_pull_request",
  deliverMessage: "driver.deliver_message",
} as const;
