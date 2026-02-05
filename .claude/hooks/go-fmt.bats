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
