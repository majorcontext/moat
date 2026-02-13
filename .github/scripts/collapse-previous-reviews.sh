#!/usr/bin/env bash
#
# Collapses previous Claude Code review comments on a PR by wrapping them
# in a <details> block. This keeps the PR timeline clean when new reviews
# supersede old ones.
#
# Required environment variables:
#   GH_TOKEN    - GitHub token with pull-requests:write permission
#   REPO        - Repository in owner/repo format
#   PR_NUMBER   - Pull request number
#
set -euo pipefail

# Claude's review comments are identified by:
# 1. Being posted by claude[bot] or github-actions[bot] (depends on how the action is configured)
# 2. Containing a review heading ("## ... Review" / "### ... Review") anywhere in the body,
#    OR starting with the track_progress format ("**Claude finished")
# This handles both direct posting and track_progress comment styles.
COLLAPSED_MARKER="<!-- collapsed -->"

# Get all comments from Claude that contain a review heading and haven't been collapsed
comments=$(gh api "/repos/${REPO}/issues/${PR_NUMBER}/comments" \
  --jq '.[] | select(.user.login == "claude[bot]" or .user.login == "github-actions[bot]") | select((.body | test("##+ .*Review")) or (.body | test("^\\*\\*Claude finished"))) | select(.body | contains("<!-- collapsed -->") | not) | {id: .id, body: .body}')

if [ -z "$comments" ]; then
  echo "No previous Claude reviews to collapse"
  exit 0
fi

# Process each comment
echo "$comments" | jq -c '.' | while read -r comment; do
  if [ -z "$comment" ] || [ "$comment" = "null" ]; then
    continue
  fi

  comment_id=$(echo "$comment" | jq -r '.id')
  original_body=$(echo "$comment" | jq -r '.body')

  # Create collapsed version with the original content inside a details block
  collapsed_body="${COLLAPSED_MARKER}
<details>
<summary>📦 <strong>Previous Review</strong> (superseded by newer review)</summary>

${original_body}

</details>"

  echo "Collapsing previous review comment: $comment_id"
  gh api \
    --method PATCH \
    "/repos/${REPO}/issues/comments/${comment_id}" \
    -f body="$collapsed_body"
done

echo "Done collapsing previous reviews"
