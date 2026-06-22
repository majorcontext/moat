# HTTP request-body policy

Demonstrates a Keep policy that inspects the **JSON body** of outbound HTTP
requests, not just the host/method/path. The proxy buffers the request body,
scans it with `hasSecrets(params.body)`, and blocks any request whose body
carries a secret.

> **Illustrative only — not a maintained security pack.** Body rules are an
> opt-in hardening primitive. Pair them with strict `network` rules; they do not
> cover URL query parameters, request headers, response bodies, or non-HTTP
> egress.

## Run

```sh
moat run examples/policy-body
```

The demo POSTs two JSON bodies to `httpbin.org`:

- `{"token": "AKIAIOSFODNN7REALKEY"}` — contains a (fake) AWS key → **blocked**
- `{"note": "nothing secret here"}` — clean → **allowed**

## How it works

`.keep/http-body-rules.yaml` declares `scope: http` and a rule that matches the
host and the parsed body:

```yaml
- name: deny-secret-in-body
  match:
    when: "params.host == 'httpbin.org' && params.body != null && hasSecrets(params.body)"
  action: deny
```

`hasSecrets(params.body)` scans every string leaf of the JSON object recursively.

Match the host in `when` rather than an `operation:` glob: Keep lowercases the
runtime operation (`"post httpbin.org/post"`) and matches it with `path.Match`,
where `*` does not cross `/` — so `operation: "POST httpbin.org/*"` would never
match. `params.host`/`params.method`/`params.path` are the reliable surface.

## Limits

- **JSON + HTTPS only.** Bodies are inspected when `Content-Type` is
  `application/json` on intercepted HTTPS requests. Plain `http://` is not
  inspected.
- **Fail-closed, scope-wide.** Once any rule references `params.body`, every
  non-JSON, compressed (gzip), malformed, duplicate-key, or oversized body is
  denied across the whole `http` scope — not just the targeted host.
- **Authoring:** test for a populated body with `params.body != null` (an empty
  body is `null`), and verify your rule actually denies a matching request.
