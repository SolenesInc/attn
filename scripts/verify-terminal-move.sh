#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

env -u ATTN_CODEX_SHARED_PROFILE -u CODEX_THREAD_ID \
  go test -v "$@" ./internal/daemon ./cmd/attn -run \
  '^(TestTerminalMove.*|TestTerminalOpenCreatesOnlyTheExplicitNumberedDesktop|TestTerminalCommandsAddressEachTileOfASharedConversation|TestUnplacedAgentsKeepTheirTerminalAcrossPlacementAndRestart|TestTerminalCLIDesktopAndProfileReach|TestTerminalCLIUsesTransitiveAuthority|TestACommandCarryingItsTerminalsIDSpeaksAsTheSessionTheTerminalShows|TestReopenReturnsToItsNumberedDesktopEvenAfterItWasRemoved)$'
make check-prompts
go test ./internal/agent -run '^TestLegacyPromptCompatibility$'
if rg -n 'desktop_move_session|DesktopMoveSession|MoveSessionToDesktop' \
  internal/protocol internal/client; then
  exit 1
fi
