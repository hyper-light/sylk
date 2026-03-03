"""Minimal CLI that prints a greeting. Uses only the standard library."""

import argparse
import sys


def parse_args(argv=None):
    """Parse command-line arguments, returning the Namespace."""
    parser = argparse.ArgumentParser(description="Print a greeting.")
    parser.add_argument("--name", default="World", help="Name to greet (default: World)")
    return parser.parse_args(argv)


def greet(name):
    """Return a greeting string for *name*."""
    return f"Hello, {name}!"


def main(argv=None):
    """Entry point: parse args, print greeting."""
    args = parse_args(argv)
    print(greet(args.name))


if __name__ == "__main__":
    main()
