#!/bin/sh
# Python Development Environment Demo
#
# This script displays version information for all installed Python tools.

echo "=================================================="
echo "Python Development Environment"
echo "=================================================="
echo

echo "--- Python Runtime ---"
python --version
echo

echo "--- Package Manager (uv) ---"
uv --version
echo

echo "--- Linter (ruff) ---"
ruff version
echo

echo "--- Formatter (black) ---"
black --version
echo

echo "--- Type Checker (mypy) ---"
mypy --version
echo

echo "--- Test Runner (pytest) ---"
pytest --version
echo

echo "--- Git ---"
git --version
echo

echo "=================================================="
echo "Environment ready for Python development!"
echo "=================================================="
echo
echo "Example workflows:"
echo "  uv init myproject         # Initialize a new project"
echo "  uv add requests           # Add a dependency"
echo "  ruff check .              # Lint code"
echo "  ruff format .             # Format code (ruff)"
echo "  black .                   # Format code (black)"
echo "  mypy .                    # Type check"
echo "  pytest                    # Run tests"
