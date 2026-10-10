# 54. Docs Information Architecture: Published/Internal Split

Date: 2026-10-13

Status: Accepted

## Context

Issue #209: the docs had no clear index and no intentional content
organization. Pages read in the order they were edited, not for navigation:

- `docs/index.md` was only an MkDocs anchor for the marketing homepage — no
  reader ever got oriented or routed into the doc tree.
- The flat `docs/` directory mixed published user guides with internal design
  notes, implementation plans, session analyses, and reviews. The public site
  was protected by a per-file `exclude_docs` **denylist** — which had already
  gone stale: `acp-session-resume-plan.md`, `background-subagents-
  implementation-plan.md`, `escalation-and-context-enhancements-plan.md`,
  `session-management-refactor-plan.md`, and
  `session-2026-10-01-primary-overload-analysis.md` were being published
  because nobody remembered to extend the list.
- The MkDocs `nav:` mixed reference material, task guides, and architecture
  with no grouping, and nothing signposted which section an answer lives in —
  global search was the only reliable discovery path.
- Published pages linked to excluded pages (`tooling.md` → `mock-setup.md`,
  `machine-readable-output-design.md`), i.e. the site already had dead links.

## Decision

**1. Physical IA, by audience.** `docs/` is split by who reads it:

- `docs/start/` — onboarding: the documentation map (`index.md`) + getting
  started.
- `docs/guides/` — task-oriented, day-to-day use: providers, workflows,
  tooling, operations.
- `docs/reference/` — lookup material: eval, ACP integration, spec.
- `docs/adr/` — decision records (unchanged; stable, widely-linked URLs).
- `docs/internal/` — **not published**: `plans/`, `design/`, `analysis/` plus
  misc contributor notes at its root. Placement rules live in
  `docs/internal/README.md`.

**2. The boundary is the directory, not a denylist.** `exclude_docs` in
`website/mkdocs.yml` is now two patterns (`internal/`, `embed.go`). A new
internal page is excluded the moment it lands in `docs/internal/`; nothing can
leak by omission.

**3. The docs landing is a real map.** `docs/start/index.md` routes readers to
every section in one click (including a "where do I find…?" table). Each
section (`guides/`, `reference/`) has its own index page (nav
`navigation.indexes`) stating what's in it and the reading order. Nav is
grouped into tabs: Home (marketing) → Documentation → Guides → Reference →
Decisions.

**4. Cross-links obey the boundary.** Published → internal links use absolute
`github.com/scoutme/milk/blob/main/…` URLs (relative links would 404 on the
site); internal → published links are ordinary relative links. Old published
URLs keep working via `mkdocs-redirects` (`redirect_maps` in `mkdocs.yml`),
added to `website/requirements.txt`.

**5. What deliberately did NOT move.** `docs/adr/`, `docs/schema/` (the
`$id` of `stream-json.schema.json` is a public contract URL), `docs/images/`,
and `docs/index.md` (still the marketing anchor — the marketing page renders
through `template: home.html` and must not accumulate doc content).
`docs/embed.go`'s `//go:embed` list and `internal/selfdocs`' file list follow
the moved paths; the selfdocs alias test pins that the embedded lookups still
resolve.

## Consequences

- Every repo reference to a moved path (code comments, README, CLAUDE.md,
  CONTRIBUTING.md, inter-doc links) was rewritten mechanically; relative links
  among moved pages were recomputed per source/target pair.
- New pages have an obvious home: user-facing → `start/`/`guides/`/`reference/`
  (+ nav entry), internal → `internal/{plans,design,analysis}/`. Adding a page
  to `exclude_docs` is no longer a thing anyone has to remember.
- Pre-restructure site URLs (`/providers/`, `/tooling/`, …) redirect to their
  new locations; links to *excluded* pages from the old denylist era were dead
  already and now point at GitHub sources.
- The leaked internal pages are no longer on the public site — an
  information-hygiene fix that came free with the boundary.
- Cost: link bookmarks inside historical ADRs were rewritten to the new paths
  (paths in prose, not recorded decisions — the decisions themselves are
  untouched).
