#!/usr/bin/env bash
# Rebuilds research/crosschain/poc/vendor/bulletproofs, the patched crate proof-xmr is built against (its Cargo.toml
# [patch.crates-io] points there), from the published crate and the one committed patch, and refuses if either is
# not byte-for-byte what is pinned here (GAP-013). The directory is ignored by git, so this script is the only way a
# fresh clone can rebuild the prover the Monero companion runs, and the only way to check a local copy.
#
#   research/crosschain/poc/proof-xmr/fetch-bulletproofs.sh           # fetch, check, extract, patch
#   research/crosschain/poc/proof-xmr/fetch-bulletproofs.sh -check    # compare an existing copy with crate + patch
set -euo pipefail

CRATE=bulletproofs
VERSION=5.0.0
# sha256 of the .crate file; the same value is the "cksum" crates.io's own index records for this version.
SHA256=012e2e5f88332083bd4235d445ae78081c00b2558443821a9ca5adfe1070073d
URL="https://static.crates.io/crates/$CRATE/$CRATE-$VERSION.crate"

here="$(cd "$(dirname "$0")" && pwd)"
dest="$here/../vendor/bulletproofs"
patch="$here/patches/bulletproofs-5.0.0-r1cs-ctoption.patch"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

curl -sSfL -o "$work/$CRATE.crate" "$URL"
got="$(sha256sum "$work/$CRATE.crate" | cut -d' ' -f1)"
if [ "$got" != "$SHA256" ]; then
	echo "refusing: $URL has sha256 $got, pinned $SHA256" >&2
	exit 1
fi
(cd "$work" && tar --force-local -xzf "$CRATE.crate")
(cd "$work/$CRATE-$VERSION" && patch -p1 --quiet < "$patch")

if [ "${1:-}" = "-check" ]; then
	if [ ! -d "$dest" ]; then
		echo "no copy at $dest to check" >&2
		exit 1
	fi
	if diff -r --exclude=target --exclude=.git --exclude=Cargo.lock "$work/$CRATE-$VERSION" "$dest" >/dev/null; then
		echo "ok: $dest is $CRATE $VERSION (sha256 $SHA256) plus the committed patch, and nothing else"
		exit 0
	fi
	echo "MISMATCH: $dest differs from $CRATE $VERSION plus the committed patch:" >&2
	diff -r --exclude=target --exclude=.git --exclude=Cargo.lock "$work/$CRATE-$VERSION" "$dest" >&2 || true
	exit 1
fi

if [ -e "$dest" ]; then
	echo "refusing to overwrite $dest; move it away, or run with -check to compare it" >&2
	exit 1
fi
mkdir -p "$(dirname "$dest")"
cp -r "$work/$CRATE-$VERSION" "$dest"
echo "wrote $dest: $CRATE $VERSION (sha256 $SHA256) plus $(basename "$patch")"
