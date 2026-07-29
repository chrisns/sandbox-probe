# Research: does `nono audit` already do declared-vs-actual attestation?

Source ticket: `.scratch/profile-attestation/issues/03-nono-audit-command-overlap.md`

## Question

Before building a new "run a nono profile, diff empirically observed
reachable surface against declared grants" capability, confirm whether
nono already ships something that does this. DeepWiki mentioned a `nono
audit` command in passing, without detail — find the real command(s),
determine precisely what they check, and identify any integration point
(hook, output format, API) `sandbox-probe` could plug into instead of
building a fully separate mechanism.

## Answer, up front

**`nono audit` exists and is real, but it is a forensic session log, not a
policy-compliance checker.** It records what a supervised `nono run`
session did (command, timestamps, exit code, supervisor-observed events,
optional filesystem-state hashes, optional rollback snapshots) and can
cryptographically verify that the *recording itself* hasn't been tampered
with. **It never compares what was recorded against a profile's declared
grants.** Nono's own docs state this distinction explicitly: audit
recording answers "what happened?"; audit-log integrity answers "has the
recorded audit log changed?". Neither answers "did the agent violate its
declared permissions?" — that comparison does not exist anywhere in the
tool today.

The same pattern holds for the other candidate commands:

- `nono profile diff` — diffs two **declared** profiles against each
  other (e.g. `default` vs `claude-code`). It never touches a session's
  actual recorded behavior.
- `nono profile validate` — schema/structural validation of a profile
  JSON file (matches policy.json, valid `extends`, valid group
  references). Confirms well-formedness, not runtime behavior.
- `nono trust verify` — cryptographic signature verification of files
  against a trust policy (Sigstore-style provenance/integrity). Confirms
  authenticity, not behavior.
- `nono why` — a **static policy simulator**: given a profile/context and
  a hypothetical path/host/command, it answers "would this be
  allowed or denied?" without ever running anything. It evaluates the
  declared policy logic, not observed runtime behavior. (`--self` lets a
  process *already inside* a sandbox query its own live capability set,
  which is the closest thing nono has to runtime introspection, but it's
  a self-service query API for the sandboxed process, not an external
  comparison against declared grants.)

This confirms the map's premise exactly: nono has provenance (Sigstore
signing via `nono trust`), schema validation (`nono profile validate`),
tamper-evidence for its own logs (`nono audit verify`), and a declarative
policy diff (`nono profile diff`) — but **no command anywhere computes
declared-but-unreachable or reachable-but-undeclared**. The gap this
project's map wants to fill is real and unaddressed by nono's own
tooling, confirmed against nono's primary docs rather than DeepWiki's
passing mention.

## `nono audit` in detail

Primary source: `nono.sh/docs/cli/features/audit.md` ("Session tracking,
filtering, and compliance reporting"), corroborated by the installed
binary's own `--help` (`nono 0.68.0`, `/opt/homebrew/bin/nono`) and an
empirical test run (`nono run --silent --allow-cwd -- sh -c '...'`,
session `1a47d5bf5f6926f4`).

Subcommands (`nono audit --help`):

```
list     List all sandboxed sessions
show     Show audit details for a session
verify   Verify audit integrity by recomputing hashes from the event log
cleanup  Remove old audit sessions
```

Every `nono run` session is recorded by default into an append-only
`audit-events.ndjson` file plus a `session.json` summary, stored under
`$XDG_STATE_HOME/nono/audit/` (default `~/.local/state/nono/audit/`).
What gets recorded, per the docs table:

| Field | Meaning |
| --- | --- |
| Command | argv, with best-effort secret redaction |
| Timestamps | start, end, duration |
| Exit code | how the process terminated |
| Audit events | session start/end, supervisor-observed capability decisions, URL-open events |
| Network events | proxy audit log, when the network proxy is active |
| Tracked paths | writable policy roots for the session |
| Merkle roots | filesystem-state commitments (opt-in via `--audit-integrity`) |
| Snapshots | content-addressable rollback metadata (opt-in via `--rollback`) |
| Audit integrity summary | hash-chain head + Merkle root over the event stream (on by default, disable with `--no-audit-integrity`) |

Empirical confirmation — `nono audit show 1a47d5bf5f6926f4 --json` after a
session that wrote a file, read `/etc/hosts`, and made an outbound `curl`
(network was left open, no `--block-net`):

```json
{
  "session_id": "1a47d5bf5f6926f4",
  "command": ["sh", "-c", "echo hi > ./ok.txt; cat /etc/hosts >/dev/null; curl -s -m 3 https://example.com >/dev/null; echo done"],
  "executable_identity": {"resolved_path": "/bin/sh", "sha256": "ad5c19..."},
  "tracked_paths": [],
  "exit_code": 0,
  "merkle_roots": [],
  "network_events": [],
  "command_policy_events": [],
  "audit_event_count": 2,
  "audit_integrity": {"hash_algorithm": "sha256", "event_count": 2, "chain_head": "...", "merkle_root": "..."},
  "snapshots": []
}
```

Note `network_events: []` and `tracked_paths: []` despite the session
actually reading a file and making a network call — this run used
`--allow-cwd` with no profile, no `--audit-integrity`, and no network
proxy active, so nono had nothing declared to compare against and nothing
routed through its proxy to log. This is itself evidence for the finding
below: **the audit log's content depends entirely on what was declared
and which optional layers were turned on; it does not independently
observe and reconcile actual access against a policy unless the caller
wires that policy in as `--profile`/proxy config, and even then nono
itself does not do the declared-vs-observed diff.**

`nono audit verify` confirms the *record* is authentic — DSSE signature
(if `--audit-sign-key` was used), the attested Merkle root against the
stored integrity summary, and session-ID binding. It is explicitly scoped
(per the docs' own "Limits" section) to: supervisor-recorded events only
(not the sandboxed child's own view), a single session's event stream,
and — critically — "The audit trail is intentionally narrow in what it
claims to prove" with an explicit list of what it does *not* prove,
including that it "does not capture rollback objects or restore data"
without `--rollback`, and shared libraries/interpreters/dynamically
loaded modules are never committed. Nothing in the Limits section, the
Use Cases section (Debugging / Compliance / Forensics), or the command
reference mentions comparing recorded events to a profile's declared
grants.

## `nono profile diff` in detail

Primary source: `nono.sh/docs/cli/features/profile-introspection.md`
("Inspect, compare, and validate nono profiles and the policy rules they
reference"), corroborated by `nono profile --help`.

`nono profile diff <PROFILE1> <PROFILE2>` "Compare[s] two profiles
side-by-side. Shows additions, removals, and changes across all profile
sections: groups, filesystem, policy patches, security settings,
network, hooks, rollback, open URLs, env credentials, and custom
credentials." Both arguments are profile names or file paths — i.e. two
**declared** JSON documents. Example from the docs:

```
nono profile: diff 'default' vs 'claude-code'

  Groups:
    + claude_code_macos
    ...
  Filesystem:
    + allow $HOME/.claude
  Workdir:
    - access: None
    + access: ReadWrite
```

There is no third input representing an observed/audited session, and no
mode that takes a session ID. `nono profile validate` is schema-level
only (JSON syntax, `extends` resolves, group references exist, required
groups aren't excluded) — explicitly a pre-runtime check ("Catches common
mistakes before they cause runtime failures"), suitable for CI, but again
never touches actual execution.

## `nono why` in detail

Primary source: installed binary's own `nono why --help` (no separate
docs.nono.sh page was found for this command via the doc site's listed
CLI feature pages; the `--help` text is nono's own authoritative
reference for it, consistent with how the project's own prior research —
`docs/research/firejail-nono-srt-flag-audit.md` — used `nono run --help`
as the primary source for flag defaults).

`nono why` answers "would this be allowed or denied" for a given
path/op, host/port, command+argv, or Landlock scope, evaluated against a
supplied profile/context (`--profile`, `--allow`, `--block-net`, etc.) —
a pure policy-engine query, nothing executes. The one partial exception:
`--self` "Query current sandbox state (use inside a sandboxed process)" —
this lets an already-running sandboxed process ask nono's own enforcement
layer about its live capability set from inside the sandbox. That's
runtime introspection, but it's a self-check callable only by the
sandboxed process itself; it is not a mechanism for an external tool to
run a profile and then diff the profile's declared grants against what
that run actually touched.

## Integration points for an external tool (question 4)

Nothing in nono's docs describes a plugin/extension API, webhook, or
event-stream subscription aimed at third-party tooling. What does exist,
usable by `sandbox-probe` as data sources rather than as an API contract:

- **`--json` on `audit list/show/verify` and all `profile` subcommands**
  — explicitly documented as "suitable for ingestion by compliance tools
  or log aggregators" / "integration with scripts, CI pipelines, and
  tools like `jq`". This is nono's actual sanctioned integration surface.
- **`audit-events.ndjson`** — the raw append-only per-session event file
  on disk (`$XDG_STATE_HOME/nono/audit/`), readable directly without
  going through the CLI at all.
- **Profile JSON itself** (`nono profile show <name> --json`, or the
  registry pack's raw file) — the declared-grants side of the diff
  `sandbox-probe` wants to compute is already machine-readable this way,
  which is useful (no need to reverse-engineer the schema) but is not a
  "hook" in the sense the ticket asked about.
- **Profile `hooks`** — a field in the profile schema itself (visible in
  `nono profile diff`'s example output: `Hooks: + claude-code`), but per
  web-search corroboration these are lifecycle hooks nono installs into
  the *agent* (e.g. Claude Code's `PostToolUseFailure` hook, which fires
  when a tool call hits a sandbox denial and injects context back into
  the agent's conversation). This is agent-UX integration, not an
  attestation/verification API — not load-bearing for this project's
  use case, noted for completeness only.
- **Core library / SDKs** (`nono.sh/docs/core/overview`, Go/TS/Python
  SDK docs) — reviewed the Core Library overview; it exposes
  `CapabilitySet`/`Sandbox` construction and enforcement primitives (plus
  C FFI bindings) for *building* a sandboxed run, not for reading audit
  data or subscribing to events. No runtime-monitoring or
  declared-vs-observed comparison API was found there either.

**Conclusion for question 4**: the practical integration point is "read
nono's own JSON exports" (declared profile via `profile show --json`,
observed session via `audit show --json` / raw `audit-events.ndjson`),
not any hook or plugin API — nono doesn't have one aimed at this problem.
`sandbox-probe` would still need to build the diffing logic itself; it
just doesn't need to reverse-engineer nono's on-disk formats, since both
sides of the comparison are already documented, machine-readable outputs.

## Summary

| Command | What it checks | Declared-vs-actual? |
| --- | --- | --- |
| `nono audit list/show` | Records what a session's supervisor observed (command, exit code, optional fs/network events) | No — records observations, does not compare to a profile |
| `nono audit verify` | Cryptographic integrity of the audit record itself (hash chain, Merkle root, optional DSSE signature) | No — proves the log wasn't tampered with, not that policy was followed |
| `nono profile diff` | Structural diff between two declared profile JSON documents | No — both inputs are declarations, neither is an observed run |
| `nono profile validate` | JSON schema / `extends` / group-reference correctness | No — pre-runtime, structural only |
| `nono trust verify` | File signature vs. trust policy (Sigstore-style) | No — provenance/integrity, not behavior |
| `nono why` | Static "would X be allowed" query against a profile/context | No — policy simulation, nothing executes |
| `nono why --self` | Live capability query from inside a running sandboxed process | Partial — real runtime introspection, but self-service only, not an external declared-vs-observed diff |

No nono command, at any layer of the CLI, computes declared-but-
unreachable or reachable-but-undeclared findings. The map's premise
holds under direct verification against nono's own primary docs and the
installed binary: nono has provenance (`trust`), schema validation
(`profile validate`), tamper-evident logging (`audit verify`), and
declarative diffing (`profile diff`) — the empirical, behavioral
attestation gap this project wants to fill is unaddressed by nono itself.

## Sources

- `nono.sh/docs/cli/features/audit.md` — "Audit Trail" (full page fetched
  verbatim)
- `nono.sh/docs/cli/features/profile-introspection.md` — "Profile
  Introspection" (full page fetched verbatim)
- `nono.sh/docs` homepage and `nono.sh/docs/llms.txt` (doc-site index,
  used to enumerate CLI feature pages)
- `nono.sh/docs/core/overview` — Core Library overview
- Installed binary: `nono --version` → `0.68.0`
  (`/opt/homebrew/bin/nono`); `nono --help`, `nono audit --help`, `nono
  profile --help`, `nono trust --help`, `nono why --help`, `nono inspect
  --help`, `nono logs --help`
- Empirical test: `nono run --silent --allow-cwd -- sh -c 'echo hi >
  ./ok.txt; cat /etc/hosts >/dev/null; curl -s -m 3
  https://example.com >/dev/null; echo done'`, session
  `1a47d5bf5f6926f4`, then `nono audit list`, `nono audit show
  1a47d5bf5f6926f4 --json`, `nono inspect --json --events
  1a47d5bf5f6926f4`
- `github.com/nolabs-ai/nono` (repo confirmed reachable at both
  `nolabs-ai/nono` and the legacy `always-further/nono` slug — same
  content, consistent with the map's note that the namespace moved)
- This project's own prior research, `docs/research/firejail-nono-srt-flag-audit.md`,
  cited only for its established precedent of using nono's own `--help`
  output as a primary source when a docs-site page doesn't exist for a
  given subcommand
