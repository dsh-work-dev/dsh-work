# Workspace Agent Rules

## Toolchain and sandbox

- Do not proactively set or relocate `GOPROXY`, `GOCACHE`, `GOMODCACHE`, `PNPM_HOME`, pnpm store, npm cache, or equivalent tool caches.
- When a command must run in the sandbox, use the machine's already-resolved Go, Node, npm/pnpm, Wails, and related toolchain with its native environment.
- If sandbox restrictions block a required command, preserve the exact evidence and either use a safe non-path-changing alternative or request the required escalation; do not work around the restriction by changing cache/store paths.

## Dependency-first implementation

- Prefer mature, actively maintained community libraries for general-purpose functionality before writing infrastructure code from scratch.
- Before replacing a library with custom code, compare its API, maintenance, security, platform coverage, and lifecycle guarantees against the required contract.
- Custom implementation is justified only when the available library cannot provide a required capability, boundary, or safety guarantee; record that evaluation near the affected architecture or dependency decision.

## UI copy and content

- User-facing UI copy must describe only an action, state, result, or decision the user needs.
- Do not expose agent guidance, implementation instructions, product rules, architecture constraints, acceptance criteria, security policy, or internal notes as UI copy.
- Prefer concise labels and one short sentence where context is needed. Remove explanations that repeat information already visible in the same view.
- Put detailed rationale, constraints, and operating guidance in project documentation, not in the product UI.

## RED project knowledge

- Use `red.toml` as the authoritative mapping of Research, Evolve and Document.
- Put decision-blocking unknowns and evidence in `.research/`.
- Put proposed or active changes in `.evolve/`; Document contains accepted
  project knowledge and truthful as-built records.
- Code, tests, configuration and runtime output are implementation evidence. A
  conflict with Document must be investigated instead of silently choosing one.
- This repository keeps `.research/` and `.evolve/` local and out of Git. Keep the
  repository RED Skill and `red.toml` tracked.
- Run `red check --json` after changing RED configuration or artifacts.
