#!/bin/sh
# Playwright Testing Environment Demo
#
# Verifies that pnpm and playwright are ready for non-interactive use.

echo "=================================================="
echo "Playwright Testing Environment"
echo "=================================================="
echo

echo "--- Node ---"
node --version
echo

echo "--- pnpm (via corepack) ---"
pnpm --version
echo

echo "--- Playwright ---"
npx playwright --version
echo

echo "--- Chromium ---"
npx playwright install --dry-run chromium 2>&1 | head -3
echo

echo "--- Environment ---"
echo "  COREPACK_ENABLE_DOWNLOAD_PROMPT=$COREPACK_ENABLE_DOWNLOAD_PROMPT"
echo "  PLAYWRIGHT_BROWSERS_PATH=$PLAYWRIGHT_BROWSERS_PATH"
echo

echo "=================================================="
echo "Environment ready for browser testing!"
echo "=================================================="
echo
echo "Example workflows:"
echo "  pnpm init                           # Initialize project"
echo "  pnpm add -D @playwright/test        # Add playwright"
echo "  pnpm exec playwright test           # Run tests"
echo "  pnpm exec playwright codegen        # Generate tests"
