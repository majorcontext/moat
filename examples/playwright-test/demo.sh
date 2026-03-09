#!/bin/sh
# Playwright Testing Environment Demo
#
# Sets up a project with playwright, launches headless Chromium,
# renders a page, and takes a screenshot.

echo "=================================================="
echo "Playwright Testing Environment"
echo "=================================================="
echo

echo "--- Versions ---"
echo "  node:       $(node --version)"
echo "  pnpm:       $(pnpm --version)"
echo "  playwright: $(npx playwright --version)"
echo

echo "--- Environment ---"
echo "  COREPACK_ENABLE_DOWNLOAD_PROMPT=$COREPACK_ENABLE_DOWNLOAD_PROMPT"
echo "  PLAYWRIGHT_BROWSERS_PATH=$PLAYWRIGHT_BROWSERS_PATH"
echo

echo "--- Setting up project ---"
cd /tmp
npm init -y --silent
npm install playwright --silent
echo "  Installed playwright locally"
echo

echo "--- Running browser test ---"
node /workspace/test.js
