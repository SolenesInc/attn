#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/test-go.sh --packages -v -run '^Test(APluginServer|AnAppServerConversationHasOneHarness)' ./internal/daemon
./scripts/test-go.sh --packages -v -run '^Test(AnInstalledServerPluginKeepsConversationOwnershipThroughRegistrationAndRestarts|APlainShellCommandDoesNotReadTheHarnessCatalog|APluginServerSurvivesItsStartingAliasUninstallAndDaemonRestart|ThePluginSDKPassesItsOwnTests)$' ./cmd/attnd
