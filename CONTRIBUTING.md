# Contributing

Thanks for helping improve Orako Core.

## Before opening a pull request

1. Search existing issues and pull requests.
2. Open an issue before large behavior, schema, or public API changes.
3. Keep changes focused and preserve the ports-and-adapters boundaries described
   in `README.md`.
4. Add or update tests for behavior changes.
5. Do not include secrets, customer data, generated local state, or private SaaS
   implementation details.

## Development checks

```sh
go test ./...
golangci-lint run

cd web
npm ci
npm run build
```

Database-backed tests require PostgreSQL and `ORAKO_INTEGRATION=1`.

## Generated code

- Edit `proto/`, then run `task proto`.
- Edit SQL migrations or queries, then run `task sqlc`.
- Commit the generated outputs with the source change.

## Licensing

By submitting a contribution, you agree to the terms in [CLA.md](CLA.md). Add
the pull-request template checkbox confirming that agreement. Contributions are
also licensed under the license of the files they modify.

## Review

Maintainers may request design changes, tests, documentation, or a smaller
scope. A pull request can be declined when it conflicts with the product
direction, security posture, or maintainability goals.
