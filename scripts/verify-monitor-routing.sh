#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

tests=$(rg --no-filename '^func Test' \
  internal/daemon/monitor*_wire_test.go \
  internal/daemon/session_title_wire_test.go \
  internal/daemon/session_cost_wire_test.go \
  internal/daemon/session_transcript_wire_test.go \
  internal/daemon/state_explain_wire_test.go \
  internal/daemon/agent_peek_wire_test.go \
  internal/daemon/annotation_delivery_wire_test.go \
  internal/daemon/asks_wire_test.go \
  internal/daemon/inbox_attempts_wire_test.go \
  internal/daemon/quiet_notifications_wire_test.go \
  internal/daemon/auto_settle_wire_test.go \
  internal/daemon/snooze*_wire_test.go \
  internal/daemon/activity*wire_test.go \
  internal/daemon/crew_lifecycle_wire_test.go \
  internal/daemon/session_pull_requests_wire_test.go \
  internal/daemon/session_pull_request_refresh_wire_test.go \
  internal/daemon/desktop_answer_wire_test.go \
  internal/daemon/profiles_protocol_test.go \
  | sed -E 's/^func (Test[^ (]+).*/\1/' | paste -sd '|' -)
go test -v ./internal/daemon -run "^($tests)$" -count=1
go test ./internal/sessionmonitor ./internal/sessioncost
go test -v ./cmd/attn -run '^(TestAgentPeekShowsASessionWithoutInterruptingIt|TestSessionTranscriptAndInstructionsReadTheTargetConversation|TestStateExplainReplaysEachClaimAndWhatBecameOfIt|TestSessionSnoozeCLIShowsBatchDeadlinesAndUnsnoozes|TestAnAgentOpensAndSettlesItsOwnTurnFromItsSession|TestAnAgentVentsOncePerTurnAndWithdrawsWithoutWritingTheOutbox)$' -count=1
