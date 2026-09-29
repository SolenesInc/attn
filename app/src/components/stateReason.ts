// Only reasons that reach `unknown` have specific explanations.
export function describeUnknownReason(reason: string | undefined): string | undefined {
  switch (reason) {
    case 'stuck':
      return 'Stuck — the agent has stopped reporting anything at all';
    case 'no_evidence':
      return 'No signal from this agent yet';
    default:
      return undefined;
  }
}
