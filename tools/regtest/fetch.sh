#!/usr/bin/env bash
# Download the pinned Monero and Zebra release artifacts into vendor/regtest/dl and verify them, failing closed:
#
#   Monero  hashes.txt must carry a good PGP signature by binaryFate — the key fetched from monero-project/monero
#           utils/gpg_keys/binaryfate.asc, whose fingerprint must equal MONERO_SIGNER_FPR in pins.env — and must list
#           the tarball with the pinned sha256; the tarball must hash to it.
#   Zebra   SHA256SUMS must carry a valid sigstore bundle issued to the Zcash Foundation's release workflow
#           (ZEBRA_SIGSTORE_IDENTITY, GitHub Actions OIDC), verified with sigstore-python in WSL, and must list the
#           tarball with the pinned sha256; the tarball must hash to it.
#
#   tools/regtest/fetch.sh             fetch what is missing, verify everything
#
# The GnuPG home is vendor/regtest/gnupg (never the user's keyring). sigstore-python is installed on first use into
# $HOME/.dmz-regtest/sigstore inside WSL (pip --target; nothing system-wide).
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

need_wsl
DL="$VENDOR/dl"
mkdir -p "$DL"
get() { [ -f "$DL/$2" ] || { say "downloading $1"; curl -sSfL -o "$DL/$2.part" "$1" && mv "$DL/$2.part" "$DL/$2"; } || die "download failed: $1"; }

# --- Monero ---
get "$MONERO_HASHES_URL" hashes.txt
get "$MONERO_SIGNER_KEY_URL" binaryfate.asc
export GNUPGHOME="$VENDOR/gnupg"
mkdir -p "$GNUPGHOME" && chmod 700 "$GNUPGHOME"
gpg --batch --quiet --import "$DL/binaryfate.asc" 2>/dev/null || die "could not import binaryfate.asc"
fpr="$(gpg --batch --with-colons --fingerprint 2>/dev/null | awk -F: '$1=="fpr"{print $10; exit}')"
[ "$fpr" = "$MONERO_SIGNER_FPR" ] || die "binaryfate.asc has fingerprint $fpr, pinned $MONERO_SIGNER_FPR"
status="$(gpg --batch --status-fd 1 --verify "$DL/hashes.txt" 2>/dev/null || true)"
echo "$status" | grep -q "^\[GNUPG:\] VALIDSIG $MONERO_SIGNER_FPR " ||
	die "hashes.txt does not carry a valid signature by $MONERO_SIGNER_FPR"
say "hashes.txt: good PGP signature by binaryFate ($MONERO_SIGNER_FPR)"
grep -q "^$MONERO_TARBALL_SHA256  $MONERO_TARBALL\$" "$DL/hashes.txt" ||
	die "the signed hashes.txt does not list $MONERO_TARBALL with the pinned sha256 (a new release? update pins.env and PINS.md)"
get "$MONERO_TARBALL_URL" "$MONERO_TARBALL"
check_sha256 "$DL/$MONERO_TARBALL" "$MONERO_TARBALL_SHA256" "$MONERO_TARBALL"
say "$MONERO_TARBALL: sha256 matches the signed list"

# --- Zebra ---
get "$ZEBRA_SUMS_URL" SHA256SUMS
get "$ZEBRA_SIGSTORE_URL" SHA256SUMS.sigstore.json
check_sha256 "$DL/SHA256SUMS" "$ZEBRA_SUMS_SHA256" "Zebra's SHA256SUMS"
SIGPY='$HOME/.dmz-regtest/sigstore'
if ! wsl_exec bash -c "PYTHONPATH=$SIGPY python3 -m sigstore --version" >/dev/null 2>&1; then
	say "installing sigstore-python into WSL $SIGPY (pip --target)"
	wsl_exec bash -c "pip3 install -q --target $SIGPY 'sigstore>=4,<5'" || die "could not install sigstore-python in WSL"
fi
wsl_exec bash -c "cd '$(to_wsl "$DL")' && PYTHONPATH=$SIGPY python3 -m sigstore verify identity --bundle SHA256SUMS.sigstore.json \
	--cert-identity '$ZEBRA_SIGSTORE_IDENTITY' --cert-oidc-issuer '$ZEBRA_SIGSTORE_ISSUER' SHA256SUMS" ||
	die "Zebra's SHA256SUMS sigstore bundle did not verify against $ZEBRA_SIGSTORE_IDENTITY"
say "SHA256SUMS: sigstore bundle verified (identity $ZEBRA_SIGSTORE_IDENTITY)"
grep -q "^$ZEBRA_TARBALL_SHA256  $ZEBRA_TARBALL\$" "$DL/SHA256SUMS" || die "SHA256SUMS does not list $ZEBRA_TARBALL with the pinned sha256"
get "$ZEBRA_TARBALL_URL" "$ZEBRA_TARBALL"
check_sha256 "$DL/$ZEBRA_TARBALL" "$ZEBRA_TARBALL_SHA256" "$ZEBRA_TARBALL"
say "$ZEBRA_TARBALL: sha256 matches the signed list"
say "all artifacts verified; monero-up.sh and zcash-up.sh extract them on first start"
