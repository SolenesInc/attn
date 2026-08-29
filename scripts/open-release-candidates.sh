#!/usr/bin/env bash
set -euo pipefail

gh api --paginate --method GET \
  'repos/{owner}/{repo}/pulls?state=open&base=main&per_page=100' \
  --jq '.[] | select(.head.ref | test("^(release/v[0-9]+\\.[0-9]+\\.[0-9]+|hotfix/.+)$")) | [.head.ref, .html_url] | @tsv'
