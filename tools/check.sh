#!/usr/bin/env bash
# check.sh: the full validation in one command, step for step what the coordinator runs before a commit (W-05).
#   tools/check.sh                            # every step below
#   tools/check.sh --only=gofmt,vet,js        # a subset, by step name
#   tools/check.sh --skip=dvm                 # everything but these
#   tools/check.sh --ui                       # also both UI passes: needs PW_DIR, the simulator and tools/serve on :8082
#   tools/check.sh --verifydeploy             # also verifydeploy against the simulator (daemon 127.0.0.1:20000)
#
# Steps: dvm gofmt vet test fuzz-xmrswap fuzz-zecswap js genconfig hygiene verifyvendor, and with the flags ui
# ui-mainnet verifydeploy (naming one in --only runs it too). Each prints one line: PASS or FAIL, its time and its log
# under .sim/check/ (git-ignored). The exit status is 1 when any step failed, 2 on bad usage.
#
# Notes:
# - dvm and ui take the repository locks (tools/withlock.sh), so they wait for a companion agent's run to finish.
# - genconfig rewrites dist/simulator.
# - tools/genesistx and tools/netwallet build only inside vendor/derohe (their build.sh): gofmt covers them, vet and
#   test do not.
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 2
LOG="$ROOT/.sim/check"
ONLY="" SKIP="" UI=0 VD=0
UI_URL="${UI_URL:-http://127.0.0.1:8082/index.html}"
for a in "$@"; do
  case "$a" in
    --only=|--skip=) echo "check.sh: $a needs a step name (see --help)" >&2; exit 2 ;;
    --only=*) ONLY="${a#--only=}" ;;
    --skip=*) SKIP="${a#--skip=}" ;;
    --ui) UI=1 ;;
    --verifydeploy) VD=1 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "check.sh: unknown argument $a (see --help)" >&2; exit 2 ;;
  esac
done
mkdir -p "$LOG"
failed=0 ran=0

named() { [[ ",$1," == *",$2,"* ]]; }
# want <step> [optional-flag]: run a core step unless --only leaves it out or --skip names it; run an optional step
# only when its flag is set or --only names it
want() {
  named "$SKIP" "$1" && return 1
  if [ -n "$ONLY" ]; then named "$ONLY" "$1"; return; fi
  [ $# -lt 2 ] || [ "$2" = 1 ]
}
step() {
  local name="$1"; shift
  local t0=$SECONDS
  ran=$((ran + 1))
  if ( "$@" ) >"$LOG/$name.log" 2>&1; then
    printf 'PASS  %-13s %5ss  .sim/check/%s.log\n' "$name" $((SECONDS - t0)) "$name"
  else
    printf 'FAIL  %-13s %5ss  .sim/check/%s.log: %s\n' "$name" $((SECONDS - t0)) "$name" "$(grep -v '^\s*$' "$LOG/$name.log" | tail -1 | cut -c1-100)"
    failed=$((failed + 1))
  fi
}

# every tools package the tools module can build (genesistx and netwallet build inside vendor/derohe)
pkgs() { (cd "$ROOT/tools" && go list ./... 2>/dev/null) | grep -v -E '^veil/tools/(genesistx|netwallet)$' | sed 's|^veil/tools|.|'; }

s_dvm()          { tools/withlock.sh dvm bash tests/run.sh -v; }
s_gofmt()        { local out; out="$(gofmt -l tools)"; [ -z "$out" ] || { echo "not gofmt-clean:"; echo "$out"; return 1; }; echo "gofmt: clean"; }
s_vet()          { cd tools && go vet $(pkgs); }
s_test()         { cd tools && go test -count=1 $(pkgs); }
# the three rounds share one -timeout: on a loaded machine they took 14 min (2026-09-12), past go test's default 10
s_fuzz_xmrswap() { cd tools && go test -count=3 -timeout 30m -run '^TestCrashFuzz$' ./xmrswap; }
s_fuzz_zecswap() { cd tools && go test -count=3 -timeout 30m -run '^TestCrashFuzz$' ./zecswap; }
s_js()           { node --test tests/js; }
s_genconfig()    { cd tools && go run ./genconfig -dist -network simulator; }
s_hygiene()      { cd tools && go test -count=1 -v ./hygiene; }
s_verifyvendor() { cd tools && go run ./verifyvendor -root ..; }
s_ui()           { : "${PW_DIR:?set PW_DIR to the directory with the pinned playwright}"; tools/withlock.sh ui env PW_DIR="$PW_DIR" node tools/uitest/ui.mjs "$UI_URL" .sim/check/ui-pass; }
s_ui_mainnet()   { : "${PW_DIR:?set PW_DIR to the directory with the pinned playwright}"; tools/withlock.sh ui env PW_DIR="$PW_DIR" node tools/uitest/ui.mjs "$UI_URL" .sim/check/ui-mainnet --mainnet; }
# factory.bas v2 has no owner (MT-J15(b)), so the simulator's factory needs no -allow: any live owner role is a failure
s_verifydeploy() { cd tools && go run ./verifydeploy -network simulator -app ../app; }

want dvm          && step dvm          s_dvm
want gofmt        && step gofmt        s_gofmt
want vet          && step vet          s_vet
want test         && step test         s_test
want fuzz-xmrswap && step fuzz-xmrswap s_fuzz_xmrswap
want fuzz-zecswap && step fuzz-zecswap s_fuzz_zecswap
want js           && step js           s_js
want genconfig    && step genconfig    s_genconfig
want hygiene      && step hygiene      s_hygiene
want verifyvendor && step verifyvendor s_verifyvendor
want ui "$UI"           && step ui           s_ui
want ui-mainnet "$UI"   && step ui-mainnet   s_ui_mainnet
want verifydeploy "$VD" && step verifydeploy s_verifydeploy

if [ "$ran" -eq 0 ]; then echo "check.sh: no step selected (see --help)" >&2; exit 2; fi
if [ "$failed" -gt 0 ]; then echo "FAIL: $failed of $ran step(s) failed"; exit 1; fi
echo "PASS: $ran step(s)"
