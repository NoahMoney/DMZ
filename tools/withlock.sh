#!/usr/bin/env bash
# withlock.sh <name> <command...> — run a command under a repository-local mutex, for the two things that must
# never run twice at once: tests/run.sh (it rebuilds vendor/derohe/veil from the tree) and the Playwright UI
# pass (one profile, one port). The lock is a directory under .sim/, so mkdir's atomicity is the mutex and a
# crashed holder leaves a directory whose age says so.
#   tools/withlock.sh dvm bash tests/run.sh -run Vault
#   tools/withlock.sh ui  node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-x
set -u
name="$1"; shift
root="$(cd "$(dirname "$0")/.." && pwd)"
lock="$root/.sim/lock-$name"
mkdir -p "$root/.sim"
waited=0
until mkdir "$lock" 2>/dev/null; do
  if [ "$waited" -ge 1800 ]; then echo "withlock: $name still held after 30 min ($lock)" >&2; exit 75; fi
  sleep 5; waited=$((waited+5))
done
trap 'rmdir "$lock" 2>/dev/null' EXIT
"$@"
