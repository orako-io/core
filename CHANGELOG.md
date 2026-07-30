# Changelog

All notable changes are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.3] - 2026-07-29

### Added

- Publish canonical self-hosting, agent connection, messaging integration,
  operations, and backup/restore guides on GitHub and orako.io.

### Security

- Scope OAuth and machine tokens to the active organization.
- Make local authentication and Community resource-limit checks transactional.
- Harden project and provider reads against cross-organization access.

## [0.1.2] - 2026-07-29

### Fixed

- Disable onboarding continuation until the selected external delivery channel has a valid member ID or completed connection.

## [0.1.1] - 2026-07-29

### Fixed

- Generate and forward the required encryption key in production self-hosted deployments.

## [0.1.0] - 2026-07-29

### Added

- Initial public release of the self-hosted Orako application.
- Remote MCP server for agent-to-human questions, follow-ups, and resolution.
- Searchable organization-scoped conversation history.
- Web dashboard with local authentication and team administration.
- Slack, Discord, Telegram, and Microsoft Teams delivery adapters.
- Community and signed-license self-hosted editions.

### Security

- Updated React Router to 8.3.0 to address GHSA-qwww-vcr4-c8h2.

[Unreleased]: https://github.com/orako-io/core/compare/v0.1.3...HEAD
[0.1.3]: https://github.com/orako-io/core/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/orako-io/core/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/orako-io/core/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/orako-io/core/releases/tag/v0.1.0
