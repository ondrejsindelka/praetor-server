# Contributing to Praetor

Thank you for your interest in Praetor!

## How to contribute

1. **Bug reports** — open an issue with reproduction steps, OS/version info, and logs.
2. **Feature requests** — open an issue describing the use case before submitting code.
3. **Pull requests** — fork the repo, create a branch, submit a PR against `main`.

## Development setup

See the repo README for build and test instructions.

## Code standards

- **Go**: `golangci-lint` clean, errors wrapped, context propagated, slog JSON logging.
- **TypeScript**: `pnpm typecheck && pnpm lint` clean, strict types, Zod for validation.
- **Bash**: `shellcheck` clean, `set -euo pipefail`, all vars quoted.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `chore:`, `docs:`, `refactor:`, `test:`

## License

By contributing, you agree that your contributions will be licensed under the same license as the project.
