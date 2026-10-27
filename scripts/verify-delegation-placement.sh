#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
./scripts/build-libghostty-vt.sh
env -u ATTN_CODEX_SHARED_PROFILE -u CODEX_THREAD_ID go test "$@" ./cmd/attn \
  -run '^Test(Delegation.*|AnInterruptedDelegationKeepsItsOutpostAcrossAHomeRestart|ADelegationNamesItsCheckoutIfItDisappearsBeforeTheMachineLaunches|DelegateResolvesRelativeCwdFromALinkedWorktree|AfterACrashADelegationNeverAdoptsAWorktreeItCannotProveItCreated)$'
env -u ATTN_CODEX_SHARED_PROFILE -u CODEX_THREAD_ID go test "$@" ./internal/daemon \
  -run '^Test(ADelegatedWorktreeProviderCanChooseItsOwnDirectory|ADelegationReportsTheProviderWorktreeWhenItsAfterCreateHookFails|ADelegatedWorktreeStartsFromTheExactBaseWhereItWasAsked|ADelegatedCheckoutThatCannotBeHonouredIsRefusedByName)$'
