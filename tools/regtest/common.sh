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
# is what keeps the WSL instance, and so the daemon, alive. Its Windows pid is written to $DATA/<name>.winpid.
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
	echo "$pid" >"$DATA/$name.winpid"
}

# stop_winpid <name>: ends the wsl.exe host of a daemon, if it is still there.
stop_winpid() {
	local name="$1" f="$DATA/$1.winpid" pid
	[ -f "$f" ] || return 0
	pid="$(cat "$f")"
	powershell.exe -NoProfile -NonInteractive -Command "Stop-Process -Id $pid -Force -ErrorAction SilentlyContinue" >/dev/null 2>&1 || true
	rm -f "$f"
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
