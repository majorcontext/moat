#!/bin/sh
# Verify HTTPS git works with only the `github` grant (issue #370).
#
# Run via: moat run ./examples/grant-github-nossh   (after `moat grant github`)
#
# Without the #370 fix this fails at the first git step with either
#   fatal: ... CONNECT tunnel failed, response 407   (git can't auth to the proxy)
# or a 401 from GitHub's git smart-HTTP endpoint      (Bearer rejected; needs Basic).
#
# Set VERIFY_REPO to an HTTPS repo URL you can push to for an end-to-end
# write test. Left unset, the script proves read access against a public repo
# (still exercises the injected github.com auth header).

PUBLIC_REPO="https://github.com/octocat/Hello-World.git"
TMP=$(mktemp -d)
ERR="$TMP/err"
fail=0

ok()  { echo "  PASS: $1"; }
bad() { echo "  FAIL: $1"; fail=1; }

echo "=================================================="
echo "HTTPS git with only the github grant (issue #370)"
echo "=================================================="
echo

echo "--- 1. git proxy auth configured by moat-init ---"
method=$(git config --system --get http.proxyAuthMethod 2>/dev/null)
echo "  http.proxyAuthMethod = ${method:-<unset>}"
if [ "$method" = "basic" ]; then
  ok "proxyAuthMethod is basic (git can clear the proxy 407)"
else
  bad "expected http.proxyAuthMethod=basic"
fi
echo

echo "--- 2. HTTPS ls-remote of a public repo (exercises the github.com auth header) ---"
if head=$(git ls-remote "$PUBLIC_REPO" HEAD 2>"$ERR"); then
  echo "  HEAD = $(echo "$head" | awk '{print $1}')"
  ok "ls-remote over HTTPS succeeded"
else
  sed 's/^/    /' "$ERR"
  bad "ls-remote over HTTPS failed (pre-#370: 407 CONNECT or 401)"
fi
echo

echo "--- 3. HTTPS shallow clone of a public repo ---"
if git clone --depth 1 "$PUBLIC_REPO" "$TMP/clone" >/dev/null 2>"$ERR"; then
  ok "clone over HTTPS succeeded"
else
  sed 's/^/    /' "$ERR"
  bad "clone over HTTPS failed"
fi
echo

if [ -n "$VERIFY_REPO" ]; then
  echo "--- 4. HTTPS clone + commit + push (write auth) on $VERIFY_REPO ---"
  branch="moat-370-verify-$$"
  if git clone --depth 1 "$VERIFY_REPO" "$TMP/wr" >/dev/null 2>"$ERR" \
     && git -C "$TMP/wr" -c user.email=verify@moat -c user.name=moat \
            commit --allow-empty -m "moat #370 verify" -q \
     && git -C "$TMP/wr" push -q origin "HEAD:refs/heads/$branch" 2>"$ERR"; then
    ok "push over HTTPS succeeded (branch $branch)"
    if git -C "$TMP/wr" push -q origin --delete "$branch" 2>/dev/null; then
      echo "    cleaned up remote branch $branch"
    else
      echo "    NOTE: could not delete remote branch $branch -- remove it manually"
    fi
  else
    sed 's/^/    /' "$ERR"
    bad "push over HTTPS failed"
  fi
  echo
else
  echo "--- 4. (skipped) set VERIFY_REPO=<https url to a repo you can push to> for a write test ---"
  echo
fi

rm -rf "$TMP"

echo "=================================================="
if [ "$fail" -eq 0 ]; then
  echo "RESULT: PASS -- HTTPS git works with only --grant github"
else
  echo "RESULT: FAIL -- see failures above"
fi
echo "=================================================="
exit "$fail"
