# Research: how to invoke nono with a named registry profile

Source ticket: `.scratch/profile-attestation/issues/01-nono-run-under-profile.md`
Map: `.scratch/profile-attestation/map.md`

## Question

The project's current `nono` row in `scan-matrix.yaml` uses ad-hoc CLI flags
(`nono run --silent --allow-cwd --allow <dir> --block-net ...`), not one of
nono's registry-published, signed profiles (e.g. `nolabs-ai/codex`). Four
things needed pinning down:

1. Exact invocation shape for running under a named registry profile, and how
   a registry profile (`nolabs-ai/codex`) resolves versus a locally-authored
   one.
2. Whether the named profile must be pre-installed, or whether `nono run`
   fetches it on demand with Sigstore verification.
3. What the command to run `sandbox-probe` itself (not Codex) under
   `nolabs-ai/codex` would look like, and whether that's even a meaningful
   thing to do — nono packs for agent clients were hinted at bundling
   agent-specific plugin wiring alongside the plain sandbox profile.
4. Whether a `nono audit` command exists and, briefly, what it checks
   (detailed treatment deferred to ticket 03).

## Answer, up front

1. **`nono run --profile <namespace>/<name> -- <command>`** (short form
   `-p`). A registry profile is named `<namespace>/<name>[@version]`
   (`nolabs-ai/codex`); a local one is a bare name resolved against built-in
   and user profiles, or a filesystem path — `-p` accepts `NAME_OR_PATH`
   uniformly.
2. **Fetches on demand.** Confirmed empirically on this machine (nono
   v0.68.0, already installed, not installed for this ticket): running
   `nono run --profile nolabs-ai/codex` with the pack completely absent
   locally triggered an automatic pull, download, and install before the
   sandbox was constructed. Verification happens on a **real** run — the CLI
   itself said as much (`Skipping pack verification on --dry-run ...; a real
   run verifies them`) — and the pull path nono.sh's own docs describe for
   `nono pull` performs Sigstore bundle verification, signer-org pinning,
   etc. Docs do not explicitly describe this on-first-use auto-fetch path
   (they only describe update-checking for already-installed packs); the
   auto-fetch is a directly observed behavior of the installed binary, not
   something read off a doc page.
3. **`nono run --profile nolabs-ai/codex -- ./bin/sandbox-probe`.**
   Partially sensible. The pack's actual sandbox policy (`policy.json`) is a
   plain OS-level capability set — Landlock/Seatbelt filesystem and network
   grants — enforced identically regardless of which binary runs under it,
   so running the probe under it is a legitimate way to observe what that
   declared grant set actually permits (exactly the declared-vs-actual
   comparison the map wants). But the pack also bundles a second,
   Codex-specific layer — plugin/marketplace registration, a fenced block
   spliced into `~/.codex/config.toml`, a `SKILL.md`, and
   PostToolUse/PermissionRequest/SessionStart hook wiring that teach the
   real Codex agent how to interpret sandbox denials. None of that is a
   sandbox mechanism, none of it is exercised by running a bare probe
   binary, and it has zero effect on what the probe can observe. A
   probe-under-`nolabs-ai/codex` run is meaningful for capability-surface
   comparison and silent about the plugin half.
4. **Yes**, `nono audit` exists (`list` / `show` / `verify` / `cleanup`).
   `nono audit verify` recomputes hashes over the local event log and checks
   Merkle-root/DSSE attestation integrity — it establishes that the audit
   record wasn't tampered with, not that the sandboxed run's actual behavior
   matched the profile's declared grants. This lines up with the map's
   existing "no runtime behavioral verification" finding rather than
   contradicting it. Full treatment left to ticket 03.

## Method

nono was already installed on this machine before this ticket started
(Homebrew, `/opt/homebrew/bin/nono`, v0.68.0) — nothing was installed for
this research. Evidence below comes from three kinds of primary source, cited
inline:

- **The installed CLI itself**: `nono --help`, `nono run --help`,
  `nono profile --help`, `nono audit --help`, `nono trust --help`,
  `nono pull --help`, `nono remove --help`, and one live (but `--dry-run`,
  non-executing) invocation, `nono run --profile nolabs-ai/codex --dry-run --
  echo hi`, plus `nono profile show nolabs-ai/codex` and inspection of the
  files that pull wrote under `~/.config/nono/packages/nolabs-ai/codex/`.
- **nono's official docs**, `nono.sh/docs` (the URL the CLI's own `--help`
  footer points at), specifically the CLI reference pages under
  `nono.sh/docs/cli/features/` and `nono.sh/docs/cli/clients/codex.md`.
- **The binary's embedded strings** (`strings` on the installed Mach-O),
  confirming real Sigstore endpoints are compiled in, cross-checked against
  the signing internals doc.

**Side effect and cleanup**: the `--dry-run` pull above still fully
installed the `nolabs-ai/codex` pack and spliced its wiring into this
machine's real `~/.codex/config.toml` and `~/.codex/plugins/` (dry-run only
skips *sandbox verification and execution*, not pack installation — see
finding 2). This was reversed immediately after with `nono remove
nolabs-ai/codex` (`reversed 6/6 wiring directive(s)`), and the empty
directory husks it left behind were removed by hand; `~/.codex/config.toml`
was confirmed to no longer contain the `nono:nolabs-ai-codex` fenced block
afterward. No sandboxed command was ever executed for real (only
`--dry-run`, and the earlier `--help` invocations).

## 1. Invocation shape and profile resolution

`nono run --help` (installed v0.68.0) lists:

```
-p, --profile <NAME_OR_PATH>
        Use a profile by name or file path
        [env: NONO_PROFILE=]
```

and its own examples section includes:

```
nono run --profile nolabs-ai/claude claude        # Use a profile
```

So the shape is `nono run --profile <name-or-path> -- <program> [args...]`
(the `--` is optional when the sandboxed program's own flags don't collide
with `nono run`'s). `-p` is overloaded across three resolution kinds:

- **Registry pack profile**: `<namespace>/<name>[@version]`, e.g.
  `nolabs-ai/codex`, `nolabs-ai/codex@0.2.0`. Source:
  `nono.sh/docs/cli/features/managing-packs.md` — "Installed packs live
  under `~/.config/nono/packages/<namespace>/<name>/`" — and confirmed by
  `nono pull --help`'s usage line, `nono pull <namespace>/<name>[@<version>]`.
- **Built-in or user-authored named profile**: a bare name resolved against
  nono's built-in profile set (`default`, `node-dev`, `go-dev`, etc. — see
  `nono profile list`) or a profile the user created with `nono profile
  init`, stored under `~/.config/nono/profiles/`.
- **A file path**: any `.json` profile file, used directly, no registry or
  named-profile lookup at all.

Confirmed live: `nono profile list` on this machine reports "12 profiles"
across two sections — "Built-in" (`default`, `bun-dev`, `go-dev`, `java-dev`,
`linux-host-compat`, `mise-dev`, `node-dev`, `openclaw`, `python-dev`,
`rust-dev`, `swival`) and "Packages" (`codex — OpenAI Codex CLI agent
(registry-managed) — from nolabs-ai/codex`, present only because the pull in
Method above installed it; removed afterward). `nono.sh/docs/cli/clients/
codex.md`'s own recommended invocation (fetched via WebFetch, quoted below)
uses the pre-migration namespace:

```
nono run --profile always-further/codex -- codex --sandbox danger-full-access --ask-for-approval on-request
```

with the stated rationale of running a *single* sandbox layer — nono
enforces the OS boundary, Codex's own `--sandbox` is set to
`danger-full-access` (i.e. disabled) so denials aren't ambiguous between two
stacked sandboxes. `nolabs-ai/codex` (the current namespace, per the map's
own namespace-migration note) resolved and pulled successfully in the
empirical test in Method, confirming the doc's example is stale on
namespace but otherwise structurally current.

## 2. Pre-installed vs. fetch-on-demand, and Sigstore verification

`nono.sh/docs/cli/features/managing-packs.md` describes `nono pull`'s
install steps explicitly:

> "On install, the CLI: 1. Fetches the pull manifest from the registry.
> 2. Downloads artifacts and matching `.bundle` signature files.
> 3. Verifies Sigstore bundles locally. 4. Checks that the signer repository
> org matches the pack namespace. 5. Pins the signer identity in a local
> lockfile. 6. Installs verified artifacts into the pack store
> (`~/.config/nono/packages/`)."

The same doc page, on `nono run --profile <name>`, only documents
update-checking behavior for packs *already* installed: "nono checks whether
any pack in the active profile's extends chain has a newer version
available and prints a one-line hint if so." It does not document what
happens when the named pack isn't installed at all.

That gap was closed empirically. On this machine, with `nolabs-ai/codex`
absent from `~/.config/nono/packages/`:

```
$ nono run --profile nolabs-ai/codex --dry-run -- echo hi
  nono v0.68.0
Profile 'nolabs-ai/codex' not found locally.

  ⬇ pulling nolabs-ai/codex
     assets/logo.png  691.32 KB  ✓
     ...(10 artifacts)...
  ✓ nolabs-ai/codex 0.2.0
     Installed at  /Users/cns/.config/nono/packages/nolabs-ai/codex

  Skipping pack verification on --dry-run (1 pack(s)); a real run verifies them
  ...
```

So `nono run --profile <name>` **does** fetch on demand — the pack need not
be pre-installed. The explicit `--dry-run` log line ("a real run verifies
them") states plainly that a non-dry-run invocation performs verification of
the freshly-pulled pack, consistent with the `nono pull` verification steps
quoted above (Sigstore bundle check + signer-org match + lockfile pin) —
`--dry-run` only skips that verification (and execution), not the fetch.

Sigstore's actual presence, not just the docs' claim of it, was confirmed
two ways:

- The pulled pack's `.nono-trust.bundle` file is a JSON array of per-artifact
  entries whose `bundle` field is `"mediaType":
  "application/vnd.dev.sigstore.bundle.v0.3+json"` wrapping a `dsseEnvelope`
  with a base64 in-toto v1 `Statement` payload — the literal Sigstore bundle
  format.
- `strings` on the installed `nono` binary itself contains compiled-in
  references to `fulcio.sigstore.dev`, `rekor.sigstore.dev`,
  `oauth2.sigstore.dev`, `tuf-repo-cdn.sigstore.dev`, and
  `timestamp.sigstore.dev` — real Sigstore keyless-signing infrastructure
  endpoints, not a docs-only claim.

`nono.sh/docs/cli/internals/signing.md` ("Sigstore-based cryptographic
attestation") describes the same three-layer structure in more depth: a DSSE
envelope wrapping an in-toto v1 Statement (binding the attestation to
specific files by SHA-256 digest, `predicateType` distinguishing
instruction-file vs. trust-policy attestations) wrapped again in a Sigstore
bundle carrying the Fulcio certificate chain and Rekor transparency-log
proof for fully offline verification, plus a note that verification timing
differs by platform: Linux (Supervised mode) re-verifies at runtime via a
seccomp-notify interceptor on `openat`/`openat2` with TOCTOU protection,
while macOS verifies only at startup.

## 3. Running `sandbox-probe` under `nolabs-ai/codex`

Command: `nono run --profile nolabs-ai/codex -- ./bin/sandbox-probe`
(add whatever probe subcommand/report-path flags are relevant; `--allow-cwd`
if the probe needs to read its own working directory beyond what the
profile already grants).

The profile's fully resolved policy, from `nono profile show
nolabs-ai/codex` on this machine:

```
Security groups:
  deny_credentials, deny_keychains_macos, deny_keychains_linux,
  deny_browser_data_macos, deny_browser_data_linux, deny_macos_private,
  deny_shell_history, deny_shell_configs, system_read_macos,
  system_read_linux_core, system_write_macos, system_write_linux,
  user_tools, homebrew_macos, homebrew_linux, dangerous_commands,
  dangerous_commands_macos, dangerous_commands_linux, codex_macos,
  user_caches_macos, node_runtime, rust_runtime, python_runtime,
  nix_runtime, git_config, unlink_protection
Signal mode:   Isolated
Capability elevation: disabled

Filesystem:
  allow (r+w): $HOME/.codex, $HOME/.agents, $NONO_CONFIG/profile-drafts
  read:        $NONO_PACKAGES, $NONO_CONFIG/profiles

Open URLs: localhost allowed, https://auth.openai.com
```

Whether this is a sensible thing to do splits into two halves, per the
ticket's own framing:

- **The sandbox-policy half is agent-agnostic and the comparison is
  meaningful.** `policy.json` (the artifact `nono run -p` actually applies)
  is a plain capability manifest — security groups plus explicit filesystem
  and network grants — enforced by nono's OS-level backend
  (Landlock/Seatbelt) the same way no matter what binary runs inside it.
  Pointing `sandbox-probe` at it observes exactly what that declared,
  externally-authored grant set permits in practice — that's the map's
  stated goal. Note the resolved policy above does carry Codex-flavored
  *content* (`$HOME/.codex`, `$HOME/.agents`, `auth.openai.com`) even though
  the *mechanism* is generic — a probe run under it will show those paths as
  reachable regardless of whether Codex is present, which is expected and
  fine: it's still the profile's true declared grant.
- **The plugin half is Codex-specific and inert for a bare probe run.** The
  pack's `package.json` describes itself as teaching "OpenAI Codex how to
  work inside a nono security sandbox," and the pulled artifacts (inspected
  directly under `~/.config/nono/packages/nolabs-ai/codex/` before removal)
  confirm a `.codex-plugin/plugin.json`, a `skills/nono-sandbox/SKILL.md`,
  and `wiring/codex-block.toml` — a TOML block the pack splices into
  `~/.codex/config.toml` giving Codex's own model explicit instructions for
  recognizing nono sandbox-denial signatures (`sandbox-exec: sandbox_apply:
  Operation not permitted`, `EACCES`, `landlock`, etc.) and refusing to
  suggest `chmod`/`sudo`/TCC workarounds. `nono.sh/docs/cli/clients/
  codex.md` additionally describes hook wiring for three Codex events
  (`PostToolUse`, `PermissionRequest`, `SessionStart`) that intercept and
  pre-load sandbox-denial context into Codex's own conversation. None of
  this is a sandbox mechanism and none of it does anything when the command
  under `nono run -p nolabs-ai/codex --` is `sandbox-probe` instead of
  `codex` — it's host-side config/plugin wiring for the real Codex CLI,
  invisible to and unexercised by any other binary.

**Conclusion**: probing under `nolabs-ai/codex` is sensible and useful for
what the map actually wants (declared-vs-actual capability-surface
comparison), but it should be documented as testing only the policy half of
the pack — it says nothing about, and can't validate, the Codex-plugin
half. That distinction is worth carrying into ticket 05's scan-matrix design
so the row's description doesn't imply full-pack coverage.

## 4. `nono audit`

Brief, per the ticket — ticket 03 covers this in depth.

`nono audit --help` (installed v0.68.0) lists four subcommands: `list`
(list all sandboxed sessions, filterable by date/command), `show` (session
detail, `--json` export), `verify` (integrity check), `cleanup` (prune old
sessions). `nono.sh/docs/cli/features/audit.md` states "Every `nono run`
session is recorded by default," logging command/args (secret-redacted),
timestamps, exit codes, capability decisions, network events from the proxy
log, tracked writable paths, and (opt-in) filesystem-state hashes
(`--audit-integrity`) and rollback snapshots (`--rollback`).

`nono audit verify` specifically performs structural/cryptographic checks
only: hash-chain and Merkle-root validation of the local event log, ledger
inclusion, and (if signed) DSSE attestation verification of "the attested
Merkle root against the session's stored audit integrity summary," plus
optional key pinning against a supplied public key. Per the docs page, this
is explicitly scoped to "session-local audit integrity and ledger
inclusion" — it proves the audit record wasn't tampered with after the
fact, not that the sandboxed process's actual behavior matched the
profile's declared grants. This is consistent with, not a contradiction of,
the map's existing "nono profiles have no runtime behavioral verification
today" finding: the audit trail documents what the supervisor observed, it
doesn't check that observation against the declared policy.

## Sources

- `nono --help`, `nono run --help`, `nono profile --help`,
  `nono profile show nolabs-ai/codex`, `nono profile list`,
  `nono audit --help`, `nono trust --help`, `nono trust verify --help`,
  `nono pull --help`, `nono remove --help` — installed nono v0.68.0
  (Homebrew, `/opt/homebrew/bin/nono`), already present on this machine
  before this ticket.
- Live (dry-run only) invocation: `nono run --profile nolabs-ai/codex
  --dry-run -- echo hi`, and inspection of the pulled pack under
  `~/.config/nono/packages/nolabs-ai/codex/` (`package.json`,
  `.codex-plugin/plugin.json`, `wiring/codex-block.toml`,
  `wiring/marketplace.json`, `skills/nono-sandbox/SKILL.md`,
  `.nono-trust.bundle`) — reverted afterward with `nono remove
  nolabs-ai/codex`.
- `strings` on `/opt/homebrew/Cellar/nono/0.68.0/bin/nono` (Sigstore
  endpoint confirmation).
- `https://nono.sh/docs` (docs index / CLI `--help` footer link)
- `https://nono.sh/docs/cli/features/managing-packs.md`
- `https://nono.sh/docs/cli/features/profiles-groups.md`
- `https://nono.sh/docs/cli/features/profile-authoring.md`
- `https://nono.sh/docs/cli/features/profile-introspection.md`
- `https://nono.sh/docs/cli/features/trust.md`
- `https://nono.sh/docs/cli/internals/signing.md`
- `https://nono.sh/docs/cli/features/audit.md`
- `https://nono.sh/docs/cli/clients/codex.md`
- `https://nono.sh/docs/llms.txt` / `https://nono.sh/docs/llms-full.txt`
  (docs site index, used to locate the pages above)
