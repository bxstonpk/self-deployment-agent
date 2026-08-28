# Company Deployment Skill

Module Z ([`../docs/02_Functional_Requirements.md`](../docs/02_Functional_Requirements.md))
per the structure defined in
[`../docs/08_Company_Deployment_Skill.md`](../docs/08_Company_Deployment_Skill.md)
— the packaged instruction set Claude Code reads to know how to behave when
an employee asks it to deploy an application through
[`../services/mcp-server/`](../services/mcp-server/).

This is a **procedural artifact, not a security control**. Every real check
(auth, ownership, policy, quota) is enforced server-side by the MCP server
and Platform API regardless of whether this skill is followed correctly —
see `SKILL.md`'s own opening paragraph. This package's only job is to make
Claude Code a well-behaved, low-friction client of that boundary.

## Package structure

```
company-deployment-skill/
  SKILL.md                              # the procedure Claude Code loads
  docs/
    supported-stack.md                  # cached runtime/db/cache catalog
    policy-summary.md                   # what's actually enforced today
    deployment-lifecycle.md             # get_deployment_status phases
    troubleshooting.md                  # error codes -> plain language
  schemas/
    deployment.schema.json              # deployment.yaml structural schema
  examples/
    frontend-only.deployment.yaml
    frontend-api.deployment.yaml
    frontend-api-db.deployment.yaml
    frontend-api-db-cache.deployment.yaml
```

Matches the structure `08_Company_Deployment_Skill.md` Section 3 specifies,
with one deliberate addition (this README) documenting the package's own
story — design decisions, real gaps found while writing it, and how it was
verified — kept separate from `SKILL.md` itself so that file stays pure
instructional content.

## Written against the real implementation, not just the spec

`docs/08_Company_Deployment_Skill.md` describes the *intended* shape of
this skill in general terms (12 tools, an async deploy-then-poll pattern,
`runtime: golang`, etc.). This package was written by reading the **actual**
Platform API and MCP server source
(`services/platform-api/internal/service/validation_service.go`,
`services/platform-api/internal/db/migrations/0002_stack_catalog_and_validation.sql`,
`services/mcp-server/src/mcp_server/`) rather than transcribing the spec
document, because — per the spec document's own stated principle (Section
5: "the MCP/Platform API's live behavior wins") — a skill that faithfully
reflects the spec's aspirations but not the real server behavior would
actively mislead the agent using it. Specific corrections made along the
way, found by reading the real code rather than assumed from the spec:

- **The runtime name is `go`, not `golang`.** The spec document's own
  worked example (Section 9) uses `runtime: golang` in its
  `deployment.yaml` sample — that value would be rejected by the real
  stack-compliance check (`internal/db/migrations/0002_...sql` seeds `go`,
  not `golang`). Every example in `examples/` and all of `SKILL.md` uses
  the real value. This is exactly the kind of drift Section 5 warns
  about, caught before shipping rather than after.
- **`deploy_application` is synchronous today**, not the
  acknowledge-then-poll async pattern the spec describes (Section 9 of
  `07_MCP_Requirements.md`) — it runs Build → Scan → Deploy → Health Check
  within one call and returns the terminal result directly (see
  `services/mcp-server/src/mcp_server/tools/deployment.py`'s module doc).
  `SKILL.md` instructs Claude Code to still call `get_deployment_status`
  as its confirmation step regardless, so the procedure keeps working
  unchanged if this becomes genuinely asynchronous later.
- **`deploy_application` takes `source_archive_base64`**, not just an
  `application_id`/`target_environment` pair — this parameter didn't
  exist when `08_Company_Deployment_Skill.md` was written; it was added
  to `services/mcp-server` specifically to close the gap where the MCP
  server had no way to trigger a build at all (see `services/mcp-server`
  and `services/platform-api`'s own READMEs for that story). `SKILL.md`'s
  deploy step reflects the real, current parameter.
- **There are 13 tools, not 12.** `07_MCP_Requirements.md` says "the 12
  business tools" in several places while Section 13 itself lists 13
  numbered entries — `SKILL.md` notes this discrepancy explicitly rather
  than silently picking one number, so it isn't a surprise later.
- **`cache:` is not a real `deployment.yaml` field.** `redis` is seeded in
  the Supported Stack catalog (so `get_supported_stacks` lists it) but
  `internal/domain/deployment_yaml.go`'s struct has no `cache` key at
  all — only `app`/`services`/`database`/`scaling`/`resources`/`domain`
  are ever accepted. `examples/frontend-api-db-cache.deployment.yaml`
  documents this honestly (confirmed for real — see below) instead of
  shipping a "working" example that would actually fail validation.

## Verified for real, not just written

- **Schema and example files parse.** `schemas/deployment.schema.json` is
  valid JSON; all four `examples/*.yaml` files are valid YAML.
- **The three real examples pass real server-side validation.**
  Registered an application per example (`team-handbook`/Engineering,
  `expense-estimator`/Finance, `overtime`/HR) against a real running
  Platform API (`docker compose`), saved each example's exact file
  content as its `deployment.yaml`, and called the real
  `POST .../validate` endpoint — all three returned `valid: true` with
  `schema`/`stack_compliance` both `passed`.
- **The documented `cache:` gap is real, not assumed.** Submitted a
  `deployment.yaml` with an aspirational `cache: {type: redis}` block
  against the real Platform API — confirmed it fails with
  `security_precheck` / `"field cache not found in type
  domain.DeploymentYAML"`, the exact failure `examples/frontend-api-db-cache.deployment.yaml`'s
  comment describes.
- **The exact error message quoted in `docs/troubleshooting.md` for a
  name mismatch is real.** Submitted `app.name: wrong-name` against an
  application registered as `session-service` and confirmed the real
  response text is character-for-character what's quoted there:
  `app.name ("wrong-name") must match the registered application name
  ("session-service")`.

## Known gaps (documented, not hidden)

- **Never dogfooded through an actual Claude Code session end-to-end.**
  This package was verified by checking its individual claims (schema
  validity, example validity, exact error text) against the real
  Platform API directly, not by handing `SKILL.md` to a fresh agent
  session with no other context and observing whether it independently
  arrives at correct MCP tool calls for a realistic employee request.
  That's a meaningfully stronger verification this package hasn't had
  yet.
- **No `api`/`worker`-only example**, matching
  `08_Company_Deployment_Skill.md` Section 10's own open decision #3
  (left open there, left open here too — not fabricated as resolved).
- **No governance/publishing mechanism.** Section 10's open decision #4
  (who approves changes to `SKILL.md`, how `docs/`/`schemas/` get
  refreshed when the platform's catalog changes) is explicitly out of
  scope for a single skill package to resolve on its own — this package
  is the artifact, not the release process around it.
- **This package's own drift risk is real.** If `services/platform-api`'s
  validation rules or stack catalog change after this was written,
  `docs/`/`schemas/` here can go stale exactly the way `SKILL.md` itself
  warns about — there is no automated check tying this package's content
  to the platform's live schema.
