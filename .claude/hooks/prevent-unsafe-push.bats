#!/usr/bin/env bats

setup() {
  HOOK="$BATS_TEST_DIRNAME/prevent-unsafe-push.py"
}

# Helper: pipe JSON to hook, capture exit code and stderr
run_hook() {
  local output
  output="$(echo "$1" | "$HOOK" 2>&1)" && status=0 || status=$?
  echo "$output"
  return "$status"
}

# --- Allowed commands ---

@test "allows push to feature branch" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git push origin my-feature"}}')" || status=$?
  [ "${status:-0}" -eq 0 ]
}

@test "allows non-git commands" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"echo hello"}}')" || status=$?
  [ "${status:-0}" -eq 0 ]
}

@test "allows non-Bash tools" {
  result="$(run_hook '{"tool_name":"Write","tool_input":{"command":"git push origin main"}}')" || status=$?
  [ "${status:-0}" -eq 0 ]
}

@test "allows push with --force-with-lease" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git push --force-with-lease origin my-feature"}}')" || status=$?
  [ "${status:-0}" -eq 0 ]
}

# --- Blocked: push to main ---

@test "blocks push to main" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git push origin main"}}')" || status=$?
  [ "${status:-0}" -eq 2 ]
  [[ "$result" == *"Direct push to main is not allowed"* ]]
}

@test "blocks push to main without explicit remote" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git push main"}}')" || status=$?
  [ "${status:-0}" -eq 2 ]
}

@test "blocks push to main in chained command" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"echo foo && git push origin main"}}')" || status=$?
  [ "${status:-0}" -eq 2 ]
  [[ "$result" == *"Direct push to main is not allowed"* ]]
}

@test "blocks --force-with-lease to main" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git push --force-with-lease origin main"}}')" || status=$?
  [ "${status:-0}" -eq 2 ]
  [[ "$result" == *"Direct push to main is not allowed"* ]]
}

# --- Blocked: force push ---

@test "blocks git push -f" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git push -f origin my-feature"}}')" || status=$?
  [ "${status:-0}" -eq 2 ]
  [[ "$result" == *"Force push is not allowed"* ]]
}

@test "blocks git push --force" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"git push --force origin my-feature"}}')" || status=$?
  [ "${status:-0}" -eq 2 ]
  [[ "$result" == *"Force push is not allowed"* ]]
}

@test "blocks force push in chained command" {
  result="$(run_hook '{"tool_name":"Bash","tool_input":{"command":"echo foo && git push --force origin my-feature"}}')" || status=$?
  [ "${status:-0}" -eq 2 ]
  [[ "$result" == *"Force push is not allowed"* ]]
}
