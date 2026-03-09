#!/bin/sh
# Playwright Testing Environment Demo
#
# Installs project dependencies, launches headless Chromium,
# renders a page, and takes a screenshot.

echo "=================================================="
echo "Playwright Testing Environment"
echo "=================================================="
echo

echo "--- Versions ---"
echo "  node:       $(node --version)"
echo "  playwright: $(npx playwright --version)"
echo

echo "--- Environment ---"
echo "  PLAYWRIGHT_BROWSERS_PATH=$PLAYWRIGHT_BROWSERS_PATH"
echo

echo "--- Installing project dependencies ---"
npm install
echo

echo "--- Running browser test ---"
node test.js
