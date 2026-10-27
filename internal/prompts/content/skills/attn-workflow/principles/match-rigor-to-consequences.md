# Match rigor to consequences

Apply before adding validation, guards, recovery paths, or other defensive machinery.

Match rigor to the software's actual requirements, operating conditions, and consequences of failure. Before adding validation, guards, recovery paths, or other defensive machinery, consider whether the failure is credible here and whether an existing boundary already handles it on every path that reaches this code. Prefer the simplest design that preserves the required behavior. A conceivable failure alone does not justify added complexity; a credible risk can justify prevention before it has ever occurred.
