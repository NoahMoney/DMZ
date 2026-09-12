# Shared helpers for the DMZ regtest harness. Sourced, never run. Git Bash on Windows; the daemons themselves run
# inside WSL2 (see README.md for why), reached from Windows at 127.0.0.1 through WSL's localhost forwarding.
#
# Nothing here knows an absolute path: the repository is found from this file's own location.

set -euo pipefail

# Git Bash rewrites anything that looks like a POSIX path in a native program's arguments ("/mnt/c/x" becomes
# "C:/Program Files/Git/mnt/c/x"). wsl.exe must receive Linux paths untouched.
export MSYS_NO_PATHCONV=1
export MSYS2_ARG_CONV_EXCL='*'

REGTEST_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$REGTEST_HERE/../.." && pwd)"
VENDOR="$REPO/vendor/regtest"
DATA="$REPO/.sim/regtest"
WSL_DISTRO="${DMZ_REGTEST_DISTRO:-Ubuntu-24.04}"

# shellcheck source=pins.env
. "$REGTEST_HERE/pins.env"

die() {
	echo "regtest: $*" >&2
	exit 1
}

say() { echo "regtest: $*"; }

# A Git Bash path (/c/Users/...) as WSL sees it (/mnt/c/Users/...). Only drive paths are supported: the repository
# must live on a Windows drive, which is where the companions run.
to_wsl() {
	case "$1" in
	/[a-zA-Z]/*) printf '/mnt/%s/%s' "$(printf '%s' "${1:1:1}" | tr 'A-Z' 'a-z')" "${1:3}" ;;
	*) die "cannot map $1 into WSL: the repository must be on a Windows drive" ;;
	esac
}

# A Git Bash path as Windows sees it (C:\Users\...), for PowerShell.
to_win() { cygpath -w "$1"; }

# scrub is a filter that writes the repository root as <repo>, in every form a path to it takes here — JSON-escaped
# C:\\..., C:\..., C:/..., WSL /mnt/c/..., Git Bash /c/... — so no tracked log carries a machine-specific path. The
# WSL form goes before the Git Bash one, which is a substring of it.
scrub() {
	perl -pe 'BEGIN { @f = grep { length } @ARGV; @ARGV = () } for my $x (@f) { s/\Q$x\E/<repo>/g }' \
		"$(cygpath -w "$REPO" | sed 's/\\/\\\\/g')" "$(cygpath -w "$REPO")" "$(cygpath -m "$REPO")" "$(to_wsl "$REPO")" "$REPO"
}

wsl_exec() { wsl.exe -d "$WSL_DISTRO" --exec "$@"; }

need_wsl() {
	command -v wsl.exe >/dev/null 2>&1 || die "wsl.exe not found: the daemons run inside WSL2 ($WSL_DISTRO)"
	wsl_exec true >/dev/null 2>&1 || die "WSL distro '$WSL_DISTRO' does not start (set DMZ_REGTEST_DISTRO to another)"
}

# check_sha256 <file> <expected hex> <what>: fail closed on a missing file or a different hash.
check_sha256() {
	local f="$1" want="$2" what="$3" got
	[ -f "$f" ] || die "$what is missing: $f (run tools/regtest/fetch.sh)"
	got="$(sha256sum "$f" | cut -d' ' -f1)"
	[ "$got" = "$want" ] || die "$what has sha256 $got, pinned $want — refusing to run it ($f)"
}

# port_listening <port>: true when something on Windows' side listens on 127.0.0.1:<port> or 0.0.0.0:<port>
# (WSL's forwarded ports show up here too).
port_listening() {
	netstat -ano 2>/dev/null | tr -d '\r' | awk -v p=":$1" '$1=="TCP" && $4=="LISTENING" && substr($2, length($2)-length(p)+1)==p {f=1} END {exit !f}'
}

# start_detached <name> <launcher-in-wsl>: starts wsl.exe running the launcher script, as a process of its own
# (created through WMI, so it is not a child of this shell and survives it), with no window. The wsl.exe process
# is what keeps the WSL instance, and so the daemon, alive. $DATA/<name>.winpid gets two lines: its Windows pid, and
# the launcher, whose path in the process's command line is what stop_winpid checks before it stops anything.
start_detached() {
	local name="$1" launcher="$2" pid
	pid="$(powershell.exe -NoProfile -NonInteractive -Command "
		\$si = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ShowWindow = [uint16]0}
		\$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
			CommandLine = 'wsl.exe -d $WSL_DISTRO --exec bash \"$launcher\"'
			ProcessStartupInformation = \$si
		}
		if (\$r.ReturnValue -ne 0) { exit 1 }
		\$r.ProcessId" | tr -d '\r')"
	[ -n "$pid" ] || die "could not start $name through WMI"
	printf '%s\n%s\n' "$pid" "$launcher" >"$DATA/$name.winpid"
}

# stop_winpid <name>: ends the wsl.exe host of a daemon, if it is still there and is still that host. A pid alone
# proves nothing: once a daemon has gone its host exits (the stagenet watchdog restarts monerod in its own WSL session,
# and the first host exits then), and Windows may give the pid to any process, another agent's node's wsl.exe included.
# So the process is stopped only if it is a wsl.exe whose command line runs `bash "<launcher>"` for the launcher
# start_detached wrote on the file's second line; anything else is left alone, and said. A file without that line was
# written before this check existed: nothing proves what its pid is now, so it is left alone too (a host exits with the
# daemon it runs, which the caller stops inside WSL).
stop_winpid() {
	local name="$1" f="$DATA/$1.winpid" pid launcher lq r
	[ -f "$f" ] || return 0
	pid="$(head -n 1 "$f" | tr -dc 0-9)" || true
	launcher="$(sed -n 2p "$f" | tr -d '\r')" || true
	rm -f "$f"
	[ -n "$pid" ] || return 0
	if [ -z "$launcher" ]; then
		say "$name: $f names pid $pid but no launcher (it predates the check), so nothing proves what that pid is now: not stopping it"
		return 0
	fi
	lq="$(printf '%s' "$launcher" | sed "s/'/''/g")" # a single-quoted PowerShell string
	r="$(powershell.exe -NoProfile -NonInteractive -Command "
		\$want = '--exec bash ' + [char]34 + '$lq' + [char]34
		\$p = Get-CimInstance -ClassName Win32_Process -Filter 'ProcessId = $pid' -ErrorAction SilentlyContinue
		if (-not \$p) { 'gone' }
		elseif (\$p.Name -eq 'wsl.exe' -and \$p.CommandLine -and \$p.CommandLine.Contains(\$want)) {
			Stop-Process -Id $pid -Force -ErrorAction SilentlyContinue; 'stopped'
		} else { 'other' }" 2>/dev/null | tr -d '\r')" || true
	case "$r" in
	stopped | gone) ;;
	other) say "$name: pid $pid is no longer the wsl.exe that runs $launcher (Windows has given the pid to another process): left alone" ;;
	*) say "$name: could not read what pid $pid is now: left alone" ;;
	esac
}

# stagenet_watchdog_pid <stagenet dir>: the pid, inside WSL, of the running stagenet watchdog
# (stagenet-watchdog.sh --run), read from the pid file its launcher wrote. It prints nothing when there is no such file,
# or when the process is not the watchdog, and never fails.
stagenet_watchdog_pid() {
	local f="$1/watchdog.pid" pid
	[ -f "$f" ] || return 0
	pid="$(tr -dc 0-9 <"$f")"
	[ -n "$pid" ] || return 0
	if wsl_exec cat "/proc/$pid/cmdline" 2>/dev/null | tr '\0' ' ' | grep -q 'stagenet-watchdog.sh --run'; then
		printf '%s\n' "$pid"
	fi
	return 0
}

# stagenet_monerod_alive <stagenet dir>: true when monerod.pid names a live process started with --stagenet.
stagenet_monerod_alive() {
	local f="$1/monerod.pid" pid
	[ -f "$f" ] || return 1
	pid="$(tr -dc 0-9 <"$f")"
	[ -n "$pid" ] || return 1
	wsl_exec cat "/proc/$pid/cmdline" 2>/dev/null | tr '\0' ' ' | grep -q -- '--stagenet'
}

# stagenet_wallet_rpc_pid <binary> <port> [pid]: the pid, inside WSL, of the one process that is the pinned
# monero-wallet-rpc as tools/regtest/stagenet-wallets-up.sh runs it: <binary> exactly as its program name; --stagenet,
# --rpc-bind-ip 127.0.0.1 and --rpc-bind-port <port> each whole arguments of its command line (pgrep -af); and its
# running image, /proc/<pid>/exe, which stays the image the process started from even if the file is replaced, hashing
# to MONERO_WALLET_RPC_SHA256. Given a pid, only that process is considered. Prints nothing when no process qualifies,
# or more than one does, and never fails: the caller decides what nothing means.
stagenet_wallet_rpc_pid() {
	local bin="$1" port="$2" want="${3:-}" pids got
	pids="$(wsl_exec pgrep -af monero-wallet-rpc 2>/dev/null | tr -d '\r' | awk -v bin="$bin" -v port="$port" -v want="$want" '
		want != "" && $1 != want { next }
		$2 != bin { next }
		{
			sn = 0; ip = 0; pt = 0
			for (i = 3; i <= NF; i++) {
				if ($i == "--stagenet") sn = 1
				else if ($i == "--rpc-bind-ip" && $(i + 1) == "127.0.0.1") ip = 1
				else if ($i == "--rpc-bind-port" && $(i + 1) == port) pt = 1
			}
			if (sn && ip && pt) print $1
		}')" || true
	case "$pids" in
	"" | *[!0-9]*) return 0 ;; # none, or more than one (the newline between two is not a digit)
	esac
	got="$(wsl_exec sha256sum "/proc/$pids/exe" 2>/dev/null | tr -d '\r' | awk '{print $1}')" || true
	[ "$got" = "$MONERO_WALLET_RPC_SHA256" ] || return 0
	printf '%s\n' "$pids"
}

# wait_for <seconds> <description> <command...>: polls once a second until the command succeeds.
wait_for() {
	local secs="$1" what="$2" i
	shift 2
	for ((i = 0; i < secs; i++)); do
		if "$@" >/dev/null 2>&1; then return 0; fi
		sleep 1
	done
	return 1
}
