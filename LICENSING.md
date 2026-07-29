# Licensing

Orako Core is **open source** under the GNU Affero General Public License v3.0
or later (AGPL-3.0-or-later). Some reusable protocol and SDK files are
Apache-2.0 as documented below. SPDX headers are authoritative where present.

## AGPL-3.0-or-later (the product) — most of the tree

The Orako server and its implementation are licensed under AGPL-3.0-or-later
(`LICENSES/AGPL-3.0-or-later.txt`). This is OSI-approved open source: you may run,
study, modify, and self-host it. The AGPL's key condition — if you run a **modified**
version to provide a network service, you must offer that service's users the
corresponding modified source — deters a competitor from taking Orako, closing their
changes, and reselling it as a hosted service, while keeping the code genuinely open
for everyone who self-hosts.

AGPL-3.0-or-later covers:

- `cmd/orako-server/` — the `orako-server` binary (community build)
- `internal/application/` — domain model, CQRS handlers, composition root,
  edition/limit enforcement
- `internal/adapters/` — driven adapters (persistence, messaging, providers,
  mail, embedder, object storage)
- `internal/infra/` — driving adapters (transport, MCP-over-HTTP, gateways,
  embedded web dashboard)
- `internal/pkg/edition`, `internal/pkg/license` — edition resolution + offline
  license-key verification
- `sql/` — schema, migrations, queries
- `web/` — the dashboard source (Vite/React)

## Apache-2.0 (the API contract + reusable SDK bits)

The wire contract and generic, product-agnostic packages stay under Apache-2.0
(`LICENSES/Apache-2.0.txt`) so integrators can build clients without AGPL
obligations:

- `proto/`, `gen/` — the `orako.v1` Connect contract and its generated bindings
- `internal/pkg/{auth,config,errs,logger,postgres,server,testsupport}` — generic
  helpers with no product logic

## Not in this repository

Two categories of proprietary code are intentionally **absent** from this
open-source repository:

- **SaaS-only** (`internal/saas/`) — code that only orako.io's hosted service runs
  (billing, analytics, and cloud operations). It lives in a separate private
  repository and is not present here.

Neither is required to run Orako. The community build in this repo is fully
functional under the free-tier caps (5 members / 1 org / 1 project). To raise those
caps, obtain a license key from the hosted licensing service.

## Contributions

See `CONTRIBUTING.md` and `CLA.md` before submitting a contribution.
