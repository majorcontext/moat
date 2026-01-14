#!/usr/bin/env python3
"""Fibonacci calculator with a bug for testing."""


def fibonacci(n: int) -> int:
    """Calculate the nth Fibonacci number.

    Args:
        n: The position in the Fibonacci sequence (0-indexed)

    Returns:
        The nth Fibonacci number
    """
    if n <= 1:
        return n
    # Bug: should be n-2, not n-3
    return fibonacci(n - 1) + fibonacci(n - 3)


def main():
    """Print the first 10 Fibonacci numbers."""
    print("First 10 Fibonacci numbers:")
    for i in range(10):
        print(f"  F({i}) = {fibonacci(i)}")


if __name__ == "__main__":
    main()
