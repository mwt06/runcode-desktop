#!/usr/bin/env bash
# Read manifests, not go list: workspaces and replace must not mask drift.
set -euo pipefail
root=${1:-.}
engine=gitlab.ouc-online.com.cn/aibase/agentloop
engine_version() {
  awk -v engine="$engine" '
    $1 == "require" && $2 == engine { print $3 }
    $1 == engine { print $2 }
  ' "$1" | tr -d '\r'
}
version=$(engine_version "$root/go.mod")
[ -n "$version" ] || { echo "go.mod: missing agentloop require" >&2; exit 1; }
for module in cmd/runcode-desktop cmd/runcode-server; do
  actual=$(engine_version "$root/$module/go.mod")
  if [ "$actual" != "$version" ]; then
    echo "$module/go.mod: agentloop ${actual:-missing}, expected $version" >&2
    exit 1
  fi
done
printf '%s\n' "$version"
