#!/bin/bash
# Publishes the current tree as ONE clean commit to GitHub (the local history is kept under a tag and never pushed:
# earlier commits carried machine-specific paths). Uses the credential git already stores for github.com; the
# token is never printed. Usage: bash tools/publish.sh <owner> <repo> [private|public]
set -euo pipefail
cd "$(dirname "$0")/.."
OWNER=${1:?owner}; REPO=${2:?repo}; VIS=${3:-public}
URL="https://github.com/$OWNER/$REPO.git"

# 1. the stored credential (git credential manager), parsed without echoing it
CRED=$(printf 'protocol=https\nhost=github.com\n\n' | git credential fill)
USER=$(printf '%s\n' "$CRED" | sed -n 's/^username=//p' | head -1)
TOKEN=$(printf '%s\n' "$CRED" | sed -n 's/^password=//p' | head -1)
[ -n "$TOKEN" ] || { echo "no github.com credential stored"; exit 1; }
LOGIN=$(curl -s -H "Authorization: token $TOKEN" https://api.github.com/user | sed -n 's/^  "login": "\([^"]*\)",$/\1/p')
echo "credential user: $USER, api login: $LOGIN"
[ "$LOGIN" = "$OWNER" ] || { echo "the stored credential is not $OWNER; refusing"; exit 1; }

# 2. create the repository if it does not exist
CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: token $TOKEN" "https://api.github.com/repos/$OWNER/$REPO")
if [ "$CODE" = "404" ]; then
  PRIV=false; [ "$VIS" = "private" ] && PRIV=true
  BODY=$(printf '{"name":"%s","description":"%s","private":%s,"has_wiki":false,"has_projects":false}' "$REPO" "DMZ: a social DEX, token launchpad and NFT platform for DERO, served from the chain (TELA)" "$PRIV")
  R=$(curl -s -H "Authorization: token $TOKEN" -H "Content-Type: application/json" -d "$BODY" https://api.github.com/user/repos)
  echo "$R" | grep -q '"full_name"' || { echo "create failed: $(echo "$R" | head -c 300)"; exit 1; }
  echo "created $OWNER/$REPO ($VIS)"
elif [ "$CODE" = "200" ]; then
  echo "$OWNER/$REPO exists"
else
  echo "unexpected status $CODE"; exit 1
fi

# 3. one clean root commit from the current HEAD tree, neutral identity
git tag -f local-history-before-publish master >/dev/null
TREE=$(git rev-parse HEAD^{tree})
export GIT_AUTHOR_NAME="$OWNER" GIT_AUTHOR_EMAIL="$OWNER@users.noreply.github.com"
export GIT_COMMITTER_NAME="$OWNER" GIT_COMMITTER_EMAIL="$OWNER@users.noreply.github.com"
ROOT=$(git commit-tree "$TREE" -m "DMZ: social DEX, token launchpad, NFT platform and federation wrapper for DERO, served from the chain

Contracts (DVM-BASIC, immutable, uint64), Go reference models and DVM-level tests, the TELA app (no framework,
no dependency), Go tools (simulator drivers, treasury bot, wrapper signer, TELA installer, headless wallets),
runbooks, research, risk register, gap matrix, backlog and handover. State as of 2026-09-06: simulator and
private-testnet verified; nothing on mainnet; wrapper backends for Monero/Zcash not implemented.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>")
echo "root commit $ROOT"

# 4. push it as main, then make it the local main with the remote configured
git remote remove origin 2>/dev/null || true
git remote add origin "$URL"
git push -q origin "$ROOT:refs/heads/main"
git branch -f main "$ROOT"
git checkout -q main
git branch --set-upstream-to=origin/main main >/dev/null
echo "published $URL (branch main); local master kept as tag local-history-before-publish"
