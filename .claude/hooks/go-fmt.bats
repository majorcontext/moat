#!/usr/bin/env bats

setup() {
  HOOK="$BATS_TEST_DIRNAME/go-fmt.py"
  TMPDIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$TMPDIR"
}

# Helper: pipe JSON to hook, capture exit code and stderr
run_hook() {
  local output
  output="$(echo "$1" | "$HOOK" 2>&1)" && status=0 || status=$?
  echo "$output"
  return "$status"
}

# --- Should format ---

@test "formats a .go file after Edit" {
  cat > "$TMPDIR/bad.go" <<'GO'
package main

func main(   ) {
fmt.Println(  "hello"  )
}
GO
  result="$(run_hook "{\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"$TMPDIR/bad.go\"}}")" || status=$?
  [ "${status:-0}" -eq 0 ]
  # gofmt should have fixed indentation
  grep -q '	fmt.Println("hello")' "$TMPDIR/bad.go"
}

@test "formats a .go file after Write" {
  cat > "$TMPDIR/bad.go" <<'GO'
package main

func main(   ) {
fmt.Println(  "hello"  )
}
GO
  result="$(run_hook "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"$TMPDIR/bad.go\"}}")" || status=$?
  [ "${status:-0}" -eq 0 ]
  grep -q '	fmt.Println("hello")' "$TMPDIR/bad.go"
}

# --- Should skip ---

@test "skips non-Go files" {
  echo "not go code {{{" > "$TMPDIR/readme.md"
  result="$(run_hook "{\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"$TMPDIR/readme.md\"}}")" || status=$?
  [ "${status:-0}" -eq 0 ]
  # File should be unchanged
  grep -q "not go code {{{" "$TMPDIR/readme.md"
}

@test "skips when file_path is empty" {
  result="$(run_hook '{"tool_name":"Edit","tool_input":{}}')" || status=$?
  [ "${status:-0}" -eq 0 ]
}

@test "skips .gob files (not tricked by partial extension)" {
  echo "binary data" > "$TMPDIR/data.gob"
  result="$(run_hook "{\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"$TMPDIR/data.gob\"}}")" || status=$?
  [ "${status:-0}" -eq 0 ]
  grep -q "binary data" "$TMPDIR/data.gob"
}

# --- gofumpt-specific behavior (when gofumpt is installed) ---

@test "applies gofumpt's stricter rules when gofumpt is available" {
  command -v gofumpt >/dev/null || skip "gofumpt not installed; hook falls back to gofmt"
  # A blank line right after a function's opening brace: gofumpt removes it,
  # plain gofmt leaves it. Independent of the module's Go version (unlike the
  # 0o octal rule), so it works on this standalone temp file.
  printf 'package main\n\nfunc main() {\n\n\tprintln("x")\n}\n' > "$TMPDIR/blank.go"
  result="$(run_hook "{\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"$TMPDIR/blank.go\"}}")" || status=$?
  [ "${status:-0}" -eq 0 ]
  # 2 blank lines in -> 1 after gofumpt (the post-brace blank is removed).
  [ "$(grep -c '^$' "$TMPDIR/blank.go")" -eq 1 ]
}
