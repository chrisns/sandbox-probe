# Research: mapping nono's profile schema to sandbox-probe's finding types

Source ticket: `.scratch/profile-attestation/issues/02-profile-schema-to-findings-mapping.md`

## Question

For each category in nono's declared `Profile` schema (filesystem grants,
network access, `env_credentials`, `workdir`, etc.), which `sandbox-probe`
`finding_type`(s) would empirically confirm the grant is actually reachable?
Flag nono grant categories the probe currently has no way to observe, and
flag probe finding types nono's schema has nothing declarative for.

## Answer, up front

Most of nono's user-facing grant surface maps cleanly to an existing probe
finding: filesystem grants → `sensitive_readable_paths`/`writeable_paths`,
network `allow_domain`/`block` → `external_host_dns_resolution`/
`external_host_connectivity`, `open_port`/`listen_port` → `tcp_ports_open`,
the (schema-drifted, see below) `unix_socket*` grants → `unix_socket_detection`,
`security.process_info_mode` → `process_detection`/`parent_process_detection`.

Two real gaps stand out. **Probe-side**: nono's `env_credentials`/`secrets`
mechanism injects credential material into the child's environment — the
probe has a `models.EnvFinding` type and a `detectSensitiveEnvVars()`
function (gitleaks-based) that look purpose-built for this, but neither is
wired into any task or `finding_type` today; it's dead code reachable only
from its own test. There is currently no finding that empirically confirms
whether an `env_credentials` grant actually landed in the child's
environment. **Nono-side**: the probe's `mounted_volumes_detections`,
`hostname_detection`, and `user_context_detection` (UID/GID remapping) have
no corresponding declaration in nono's schema at all — nono is a
policy-based mediator (Landlock/Seatbelt path rules), not a namespace/rootfs
swap, so it never declares mount topology, hostname, or UID/GID remapping
because it doesn't change any of them by design (consistent with the
existing finding in `docs/research/firejail-nono-srt-flag-audit.md` that
nono stays nested in the real host filesystem, never swapping in a fresh
rootfs).

One notable **schema/code drift** inside nono itself, found while sourcing
this: the checked-in JSON Schema
(`crates/nono-cli/data/nono-profile.schema.json`) does *not* list
`unix_socket`/`unix_socket_bind`/`unix_socket_dir`/`unix_socket_dir_bind`/
`unix_socket_subtree`/`unix_socket_subtree_bind` under `FilesystemConfig`
(`additionalProperties: false`, so a profile using these would fail
schema validation) — but the actual Rust struct
(`crates/nono-cli/src/profile/mod.rs`, lines ~156-181) and the authoring
guide (`docs/cli/features/profile-authoring.mdx`, lines 195-200) both define
and use all six fields. Treated the struct + authoring guide as ground
truth for this mapping since they're closer to actual runtime behavior; the
schema file appears to be the stale artifact.

## Sources

Primary (fetched directly from `github.com/nolabs-ai/nono`, default branch
`main`, via `gh api`, 2026-07-29 — the org migrated from `always-further` to
`nolabs-ai`, matching the note already in `.scratch/profile-attestation/map.md`):

- `crates/nono-cli/data/nono-profile.schema.json` — the authored `Profile`
  JSON Schema (2053 lines). Used for all `Profile`-level field names/types
  below unless flagged otherwise.
- `crates/nono/schema/capability-manifest.schema.json` — the *resolved*
  `CapabilitySet` schema (the ticket's "resolved into a CapabilitySet"
  layer): `version: "0.1.0"`, marked `additionalProperties: true` pending
  1.0 stabilization. This is what a profile compiles down to — no
  inheritance, no legacy aliases, no groups/composition.
- `crates/nono-cli/src/profile/mod.rs` — the Rust struct backing the
  schema; used to resolve the `unix_socket*` drift above.
- `docs/cli/features/profile-authoring.mdx` — the "Full Skeleton" example
  (`nono profile init --full` output), cross-checked against the schema.

Secondary (prose docs, used only where the JSON Schema didn't cover a
field, or for framing):

- `nono.sh/docs` landing page — index only, no schema content; the schema
  files above are more precise and were used instead, superseding the
  `deepwiki.com/always-further/nono/3.3-profile-format-reference` reference
  named in the source ticket (also now the wrong org name — see the org
  migration note above). DeepWiki's summary substantively agreed with the
  schema except that it also implied `unix_socket`/`unix_socket_bind` live
  under `filesystem`, which is correct per the struct/guide but not the
  (stale) schema file — consistent with the drift noted above, not a new
  discrepancy.

Repo-internal (this repo, worktree HEAD `302f795`):

- `README.md` "What it detects" table — authoritative `finding_type` list
  and one-line semantics.
- `pkg/tasks/tasks.go` — `finding_type` string constants and their expected
  value types (`expectedTypes`).
- `pkg/models/types.go` — the Go structs behind each `finding_type`'s
  `Value` (`ProxyConfig`, `UserIdentity`, `HostEnvironment`, `Process`,
  `EnvFinding`).
- `pkg/tasks/baseline/environment.go` — confirms `detectSensitiveEnvVars`/
  `models.EnvFinding` exist but are unwired (only referenced by
  `environment_test.go`); confirms `GetHostMounts`/`GetContainerRuntime`/
  `ActiveMechanisms` back `mounted_volumes_detections`/`sandbox_detection`.
- `docs/research/firejail-nono-srt-flag-audit.md` — prior finding that
  nono stays nested in the real host filesystem (no fresh rootfs), cited
  above to explain why nono's schema has no mount/hostname/UID fields.

## sandbox-probe's finding types (ground truth)

From `README.md` and `pkg/tasks/tasks.go` (`expectedTypes`):

| `finding_type` | Captures | Value type |
| --- | --- | --- |
| `sensitive_readable_paths` | readable security-sensitive files | `[]string` |
| `writeable_paths` | writable system/home paths | `[]string` |
| `external_host_dns_resolution` | hostnames resolvable | `[]string` |
| `external_host_connectivity` | hosts reachable over network | `[]string` |
| `tcp_ports_open` / `udp_ports_open` | locally reachable ports | `[]int` |
| `proxy_detection` | proxy config in env vars | `*models.ProxyConfig` |
| `unix_socket_detection` | visible AF_UNIX sockets | `[]string` |
| `process_detection` / `parent_process_detection` | visible processes / launching parent | `*models.Process` |
| `mounted_volumes_detections` | visible mounted filesystems | `[]string` |
| `user_context_detection` | UID/GID/EUID/EGID | `*models.UserIdentity` |
| `hostname_detection` | system hostname | `string` |
| `environment_detection` | host kernel/OS release | `*models.HostEnvironment` |
| `sandbox_detection` | detected enforcement runtime | `string` |

Not a `finding_type` today, but present as dead scaffolding —
`models.EnvFinding` (`EnvKey`/`EnvValue`/`Description`) plus
`detectSensitiveEnvVars()` in `pkg/tasks/baseline/environment.go`, which
runs gitleaks over `os.Environ()` values. Never called from any `Task.Run`.

## nono → probe mapping

| nono schema field(s) (`Profile`, unless noted) | What it declares | Probe `finding_type`(s) that observe it | Notes |
| --- | --- | --- | --- |
| `filesystem.read` / `read_file` | read-only dir/file grants | `sensitive_readable_paths` | Direct: does the granted path actually show up readable. |
| `filesystem.write` / `write_file` | write-only dir/file grants | `writeable_paths` | Direct. |
| `filesystem.allow` / `allow_file` | read+write grants | `sensitive_readable_paths` + `writeable_paths` | Both findings needed to confirm the full grant. |
| `filesystem.deny` | recursive deny, overrides allow | absence in `sensitive_readable_paths`/`writeable_paths` | "Declared-but-unreachable" case: probe should NOT see the path. |
| `filesystem.bypass_protection` | exempts a path from a *group*-level deny | same as above | Only meaningful combined with `groups.include`; can't be assessed from the profile file alone (needs the resolved `policy.json` groups). |
| `filesystem.unix_socket` / `unix_socket_bind` / `unix_socket_dir` / `unix_socket_dir_bind` / `unix_socket_subtree` / `unix_socket_subtree_bind` | AF_UNIX socket connect/bind grants (path-based) | `unix_socket_detection` | See schema-drift note above — these fields exist in the Rust struct/authoring guide, not the checked-in JSON Schema. Direct mapping once resolved. |
| `linux.af_unix_mediation` | `off`/`pathname` — whether the six fields above are even enforced on Linux | `unix_socket_detection` | Modifier, not a grant itself: when `off`, Unix sockets aren't mediated at all regardless of the grants above (compatibility mode) — the probe result should be interpreted against this flag. |
| `workdir.access` (`none`/`read`/`write`/`readwrite`) | CWD-scoped access | `sensitive_readable_paths` / `writeable_paths` (path = CWD) | Direct, but scoped: the probe would need to know the actual CWD path at scan time to attribute a finding to this field specifically rather than to `filesystem.*`. |
| `network.block` | disable all outbound network | absence of `external_host_connectivity` / `external_host_dns_resolution` findings | Direct. Resolved form: `capability-manifest` `network.mode: "blocked"`. |
| `network.allow_domain` (+ `proxy_allow`/`allow_proxy` aliases) | additional allowed domains, optionally with L7 `endpoints` method+path rules | `external_host_dns_resolution`, `external_host_connectivity` | Direct for host-level reachability. The `endpoints` (method+path) sub-filtering has **no probe equivalent** — the probe checks host reachability, not HTTP-method/path-level filtering; flagged as a probe gap below. |
| `network.network_profile` | named filter policy from an external `network-policy.json` | `external_host_dns_resolution`, `external_host_connectivity` | Same as `allow_domain` but the actual allow-set lives in a file outside the profile — mapping requires resolving that file too, can't be read off the profile alone. |
| `network.open_port` / `port_allow` / `allow_port` | localhost TCP connect+bind ports | `tcp_ports_open` | Direct. Resolved form: `capability-manifest` `network.ports.localhost`. |
| `network.listen_port` | TCP ports the child may listen on | `tcp_ports_open` | Direct (probe scans locally-open TCP ports as the child would present them). |
| — (no UDP field anywhere in `NetworkConfig` or `PortConfig`) | — | `udp_ports_open` | **Nono-side gap**: nono's schema is TCP-only for ports; the probe's `udp_ports_open` has nothing to diff against for this tool. |
| `network.dns` (resolved `capability-manifest` only; no `Profile`-level toggle found) | whether DNS resolution is allowed | `external_host_dns_resolution` | Direct in the resolved manifest. At the `Profile` authoring layer, DNS is implied by `network.block`/`allow_domain` rather than an explicit field — inferred, not confirmed by a named `Profile` field. |
| `network.upstream_proxy` / `external_proxy`, `network.no_proxy`, `network.tls_intercept` | enterprise upstream proxy passthrough, proxy bypass list, TLS interception CA lifecycle | `proxy_detection` | Partial/inferred: nono's own proxy mode injects env vars (CA bundle paths via `tls_intercept.ca_env_vars`, base URLs via `custom_credentials.*.base_url_env_var`) that `proxy_detection` (which scans `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY`/`NO_PROXY`/`SOCKS_PROXY`/PAC-url env vars per `models.ProxyConfig`) may or may not actually capture — nono's proxy env vars are custom-named (`base_url_env_var`, `ca_env_vars`), not necessarily the standard `HTTP_PROXY` family `ProxyConfig` looks for. This is genuinely ambiguous without an empirical nono run to see which env vars actually land. |
| `network.credentials` / `custom_credentials` / `credential_routes` / `credential_providers` | reverse-proxy credential injection routes | none directly; `proxy_detection` only if the injected env var happens to be a standard proxy var (see above) | See `env_credentials` gap below — this is the network-mediated sibling of the same underlying gap. |
| `env_credentials` / `secrets` | maps keystore/URI-backed secrets to environment variable names in the child | **none wired today** | **Probe-side gap.** `models.EnvFinding` + `detectSensitiveEnvVars()` exist and look purpose-built for exactly this (gitleaks over `os.Environ()` values) but are not called from any `Task.Run`, so no `finding_type` reports it. This is the single clearest capability gap for "declared-but-unreachable" diffing on the credentials axis. |
| `environment.allow_vars` / `deny_vars` / `set_vars` | environment variable filtering/injection | none directly | The probe has no finding that enumerates the child's full environment or diffs it against an allow/deny list — `environment_detection` only captures host kernel/OS release, a different (and confusingly similarly-named) thing. Flagged as a probe gap; would need a new finding type, not a repurposing of `environment_detection`. |
| `security.process_info_mode` (`isolated`/`allow_same_sandbox`/`allow_all`) | whether the child can read process info for other processes | `process_detection` / `parent_process_detection` | Direct: this is exactly the axis the probe's process visibility findings measure. |
| `security.signal_mode` | whether the child can signal other processes | none | **Probe-side gap** — the probe has no "can I signal PID X" check; `process_detection` only observes visibility, not signaling capability. |
| `security.ipc_mode` (`shared_memory_only`/`full`) | POSIX semaphore access | none | **Probe-side gap** — no IPC/semaphore-capability finding type exists. |
| `security.capability_elevation` | Linux seccomp-notify runtime elevation (supervisor can grant access to paths not in the initial set, interactively) | n/a (methodology caveat) | Not a static grant — flags that under this mode the *actually reachable* surface can exceed the profile's static declarations at any point during the run. Worth a caveat in the diffing methodology (a single point-in-time probe run may under-report reachability if elevation grants something mid-run), not a mapping gap per se. |
| `groups.include` / `groups.exclude` | named policy-group composition, resolved against an external `policy.json` | whichever finding types the resolved group's underlying filesystem/network/security fields map to (per rows above) | Indirection: groups aren't grants themselves, they expand into the categories above. Mapping requires resolving `policy.json`, which is out of scope for reading the `Profile` file alone. |
| `commands` / `command_policies` (tool-sandbox, broker-mediated exec allow/deny) | which *commands* the child is allowed to execute | none directly; loosely adjacent to `process_detection` | Different axis from process visibility — this is exec-time gating ("can this command run at all"), not "what's visible once running." No probe finding currently measures "was this command allowed to execute." |
| `hooks` / `session_hooks` | app-specific hook scripts / before-after session lifecycle scripts run with host privileges outside the sandbox | none | Out of the probe's model entirely — these run outside the sandbox boundary the probe measures, so there's nothing to empirically observe from inside a scan. Not really a "gap," just out of scope by design. |
| `rollback` / `undo` (`exclude_patterns`/`exclude_globs`) | which paths are excluded from undo snapshots | none | Snapshot/rollback bookkeeping, not a reachability grant — no mapping expected. |
| `open_urls` (`allow_origins`/`allow_localhost`) | which origins the supervisor may open on the child's behalf (OAuth flows) | none | Supervisor-mediated, happens outside the sandboxed process; not something the probe (running inside the sandbox) can observe. |
| `allow_launch_services` / `allow_gpu` | macOS LaunchServices / GPU (Metal/IOKit) access opt-ins | none | **Probe-side gap** — no GPU-device or LaunchServices-reachability finding type exists today. |
| `unsafe_macos_seatbelt_rules` | raw Seatbelt S-expressions, escape hatch | whatever finding type corresponds to the rule's actual effect (varies per rule) | Can't be mapped generically — depends entirely on what the raw rule does; would need case-by-case analysis per profile. |
| `resources.memory_bytes` / `max_processes` (resolved `capability-manifest` only; requires `exec_strategy: "supervised"`) | cgroup memory/PID ceilings | none | **Probe-side gap** — no resource-limit/ceiling finding type; the probe doesn't currently attempt to measure or exhaust resource limits. |
| — (no field) | — | `mounted_volumes_detections` | **Nono-side gap.** nono is a Landlock/Seatbelt path-mediator, not a namespace/rootfs swap — it never remounts or hides filesystems, so it has nothing to declare here by design (consistent with `docs/research/firejail-nono-srt-flag-audit.md`'s finding that nono stays nested in the real host filesystem). |
| — (no field) | — | `hostname_detection` | **Nono-side gap.** No UTS-namespace/hostname control anywhere in the schema; nono doesn't touch the hostname. |
| — (no field) | — | `user_context_detection` | **Nono-side gap.** No UID/GID/user-namespace remapping field; nono runs the child as the invoking user's own UID (no setuid/userns remapping declared anywhere in the schema). |
| — (no field) | — | `environment_detection` (host kernel/OS release) | **Nono-side gap**, but expected: this finding is about the *host* the probe ran on for longitudinal comparison, not a per-run sandbox grant — there's no reason nono's profile schema would declare anything here. |
| — (no field) | — | `sandbox_detection` | **Nono-side gap** in the sense that nono doesn't declare "I use Landlock on Linux / Seatbelt on macOS" anywhere in the profile — that's an implementation detail of the tool, not user-configurable. Still a meaningful probe check: it confirms nono's enforcement mechanism is actually active at all (per prior research, nono's own `--help` and empirical tests already establish which mechanism backs it per platform). |

## Explicit gap summary

**nono declares it, probe has no way to observe it (probe-side gaps):**

- `env_credentials`/`secrets`/`credential_routes` — credential material
  landing in the child's environment or via the reverse proxy.
  `models.EnvFinding`/`detectSensitiveEnvVars()` exist but are dead code.
- `environment.allow_vars`/`deny_vars`/`set_vars` — general environment
  variable filtering (distinct from credentials specifically).
- `security.signal_mode` — inter-process signaling capability.
- `security.ipc_mode` — POSIX semaphore/IPC capability.
- `allow_gpu` / `allow_launch_services` — GPU and macOS LaunchServices
  access.
- `resources.memory_bytes`/`max_processes` — resource ceilings.
- L7 `endpoints` (method+path) filtering under `allow_domain` /
  `custom_credentials.*.endpoint_rules` — the probe checks host
  reachability only, not HTTP method/path-level filtering.

**Probe observes it, nono's schema has nothing declarative for
(nono-side gaps — outside its declared model by design, not oversights):**

- `mounted_volumes_detections` — nono doesn't swap/remount filesystems.
- `hostname_detection` — nono doesn't touch UTS namespace/hostname.
- `user_context_detection` — nono doesn't remap UID/GID.
- `environment_detection` (host kernel/OS) — orthogonal, this is
  longitudinal host tracking, not a sandbox grant.

**Genuinely ambiguous / inferred rather than confirmed:**

- `proxy_detection` vs. nono's `tls_intercept`/`custom_credentials.*.base_url_env_var`
  proxy env vars — plausible but not confirmed which, if any, land in the
  standard `HTTP_PROXY`-family variables `models.ProxyConfig` scans for;
  would need an empirical nono run in `proxy` network mode to settle.
- `network.dns` — exists at the resolved `capability-manifest` layer but
  no explicit `Profile`-level field was found; inferred to be implied by
  `network.block`/`allow_domain` rather than independently declarable.
- `filesystem.unix_socket*` fields — real per the Rust struct and
  authoring-guide example, but absent from the checked-in JSON Schema;
  flagged as nono's own schema/code drift, not a mapping ambiguity on this
  project's side.
