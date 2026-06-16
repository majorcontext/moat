# HTTPS git with only the `github` grant

This example verifies that HTTPS `git` fetch/push to `github.com` works with **only**
`moat grant github` — no `ssh:github.com` grant required ([issue #370](https://github.com/majorcontext/moat/issues/370)).

For the SSH approach, see [`../ssh-github`](../ssh-github). For combining both, see the
[SSH guide](https://majorcontext.com/moat/guides/ssh).

## Why this was broken

The `github` grant matches two hosts that need **different** auth schemes:

- `api.github.com` (REST/GraphQL) accepts `Authorization: Bearer <token>`.
- `github.com` serves git smart-HTTP (`info/refs`, `git-upload-pack`, `git-receive-pack`),
  which **rejects Bearer with a 401** and requires
  `Authorization: Basic <base64("x-access-token:<token>")>` — the scheme GitHub Actions'
  checkout uses.

On top of that, `git` aborts on the proxy's `407` CONNECT challenge because, unlike `curl`,
it does not send `Proxy-Authorization` preemptively. The fix injects Basic for `github.com`
and sets `git http.proxyAuthMethod=basic` in the container, so HTTPS git works out of the box.

## Run it

```bash
moat grant github
moat run ./examples/grant-github-nossh
```

The script ([`verify.sh`](verify.sh)) runs entirely inside the container and prints a
`PASS`/`FAIL` line per check, exiting non-zero on any failure:

1. `git http.proxyAuthMethod` is `basic` (set by `moat-init`).
2. `git ls-remote` of a public repo over HTTPS succeeds (exercises the injected
   `github.com` Authorization header — pre-fix this returned `407` or `401`).
3. A shallow HTTPS `git clone` succeeds.
4. *(optional)* clone + empty commit + push to a scratch branch, then delete it.

### Optional write test

Prove write auth end-to-end against a repo you can push to:

```bash
moat run --env VERIFY_REPO=https://github.com/<you>/<repo>.git ./examples/grant-github-nossh
```

The script pushes an empty commit to `moat-370-verify-<pid>` and deletes that branch
afterward. A successful push is the strongest single proof, since write traffic is exactly
what GitHub's git smart-HTTP rejected under Bearer.

## Expected output

```
--- 1. git proxy auth configured by moat-init ---
  http.proxyAuthMethod = basic
  PASS: proxyAuthMethod is basic (git can clear the proxy 407)

--- 2. HTTPS ls-remote of a public repo (exercises the github.com auth header) ---
  HEAD = 7fd1a60b01f91b314f59955a4e4d4e80d8edf11d
  PASS: ls-remote over HTTPS succeeded

--- 3. HTTPS shallow clone of a public repo ---
  PASS: clone over HTTPS succeeded

--- 4. (skipped) set VERIFY_REPO=<https url to a repo you can push to> for a write test ---

==================================================
RESULT: PASS -- HTTPS git works with only --grant github
==================================================
```

## Negative control

To confirm the fix is what changed the behavior, build the binary from `main` (or any
pre-#370 build) and run the same example — checks 2 and 3 fail with
`CONNECT tunnel failed, response 407`:

```bash
git stash && git checkout main && go build -o /tmp/moat-main ./cmd/moat && git checkout - && git stash pop
MOAT_EXECUTABLE=/tmp/moat-main /tmp/moat-main run --grant github ./examples/grant-github-nossh
```

## Notes

- **Run this on a real host, not inside another Moat sandbox.** Nested in a Moat container the
  granted `GH_TOKEN` is a placeholder and the outer proxy re-intercepts `github.com`, so the
  request can't reach GitHub with your real token regardless of the fix.
- When both `github` and `ssh:github.com` are granted, git is transparently routed over SSH
  instead (an identity preference, not a workaround). Set `MOAT_GIT_SSH_GITHUB=0` to force the
  HTTPS path. See the [SSH guide](https://majorcontext.com/moat/guides/ssh).
