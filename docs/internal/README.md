# Internal docs (not published)

Everything under `docs/internal/` is **source-tree-only**: it is excluded from
the published site wholesale (a single `internal/` pattern in
`website/mkdocs.yml`, see ADR-0054) and read by contributors from the repo.

## Where new pages land

- `plans/` — implementation plans and refactoring plans
  (e.g. `plans/session-management-refactor-plan.md`).
- `design/` — design docs and internal specs that predate an ADR or go deeper
  than one (e.g. `design/machine-readable-output-design.md`).
- `analysis/` — investigations, reviews, post-mortems
  (e.g. `analysis/prompt-context-management-review.md`).
- Root — misc contributor tooling notes (`mock-setup.md`) and process
  (`branching-strategy.md`).

Published docs (`docs/start/`, `docs/guides/`, `docs/reference/`, `docs/adr/`)
may link **into** this tree only via absolute GitHub URLs — relative links
would 404 on the published site. Links from here to published docs are fine as
relative links.

The rules for where a page belongs are recorded in
[ADR-0054](../adr/0054-docs-information-architecture.md).
