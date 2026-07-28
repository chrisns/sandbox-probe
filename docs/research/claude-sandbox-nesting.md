# Research: is `claude-sandbox` (via `run-probe-via-claude-stub.sh`) nested in the seeded parent?

Source ticket: `.scratch/sandbox-canary-nesting/issues/11-claude-sandbox-nesting.md`
Map: `.scratch/sandbox-canary-nesting/map.md`

## Question

`scripts/run-probe-via-claude-stub.sh` runs the real `claude` binary (model
stubbed via `scripts/mock-agent-api.mjs`) with `CLAUDE_SANDBOX=on`, which
applies Claude Code's own OS sandbox (bubblewrap on Linux, Seatbelt on
macOS) to the Bash subprocess that runs the probe — no `docker run`/fresh
rootfs construction by our script, unlike the five broken runtimes in the
map. Is that assumption right? Empirically: can the sandboxed run's report
see the same seeded decoys the unconfined (`CLAUDE_SANDBOX=off`) run sees,
or does Claude Code's sandbox invocation reconstruct a disconnected
environment the way the original five did?

## Answer, up front

**Already correctly nested. No fix needed.** `CLAUDE_SANDBOX=on` and
`CLAUDE_SANDBOX=off` runs on this machine (macOS, Seatbelt) produced
**identical `sensitive_readable_paths`, identical hostname, identical
uid/gid, identical kernel** — the sandboxed run sees the exact same
`$HOME` decoys as the unconfined baseline, because it's the same process
tree on the same filesystem, not a fresh container. The sandbox is
genuinely active and enforcing something real (external network
connectivity dropped from `["google.com"]` to `[]`, and `sandbox_detection`
fired `"seatbelt"`) — it just doesn't restrict *reads* of `$HOME` by
default, which is a policy characteristic (matches Anthropic's own
`srt`/sandbox-runtime default: read-allow-everywhere, write-deny-by-default,
network-deny-by-default — see
[the sibling firejail/nono/srt audit](firejail-nono-srt-flag-audit.md)),
not a nesting bug. This is the `srt`/`firejail`/`nono` shape (same
filesystem, policy-restricted), not the container-disconnection shape.

**Linux/bubblewrap path not tested here** — this machine is macOS, so only
the Seatbelt half of `run-probe-via-claude-stub.sh` could be exercised.
`bwrap`'s own default posture (no formal vendor default, per
[the bwrap nesting ticket](../../.scratch/sandbox-canary-nesting/issues/03-bwrap-default-nesting.md))
means the bubblewrap path Claude Code takes on Linux is not guaranteed to
behave the same way and would need a real empirical check on a Linux box
(or a Linux VM) with `bwrap` installed, running the same script with
`CLAUDE_SANDBOX=on`/`off` and comparing reports the same way as below.

## Method

1. Read `scripts/run-probe-via-claude-stub.sh`: it runs the real `claude`
   binary against `scripts/mock-agent-api.mjs` (a stub that returns exactly
   one Bash `tool_use` running the probe, then stops) with
   `--permission-mode bypassPermissions` so the OS sandbox is the only
   constraint, and `CLAUDE_SANDBOX=on` applies
   `scripts/config/claude-code-sandbox.json`
   (`{"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false}}`)
   via `--settings`. `CLAUDE_SANDBOX=off` runs claude with no `--settings`
   at all — its default (unconfined).
2. `./scripts/seed-decoys.sh ./bin/sandbox-probe` — soft-seeds decoys at
   every `seedable` path from `sandbox-probe list-targets` under the real
   `$HOME` (`/Users/cns`), skipping anything already present. This is
   Chris's real dev machine; the script only ever writes where nothing
   already exists.
3. Ran the stub script twice, same `$HOME`, same decoys, differing only in
   `CLAUDE_SANDBOX`:
   ```
   $ export PROBE="$(pwd)/bin/sandbox-probe"
   $ export CLAUDE_SANDBOX=off OUT=.../report-off.json RUNNER=research-manual
   $ bash scripts/run-probe-via-claude-stub.sh

   $ export CLAUDE_SANDBOX=on  OUT=.../report-on.json
   $ bash scripts/run-probe-via-claude-stub.sh
   ```
4. Diffed the two `report.json`s.

## Build and seed

```
$ make build
$ ls bin/
sandbox-probe

$ ./bin/sandbox-probe list-targets | jq -r '.[] | select(.seedable) | .path' | head -5
/Users/cns/.ssh/id_rsa
/Users/cns/.ssh/id_ed25519
/Users/cns/.ssh/id_ecdsa
/Users/cns/.ssh/config
/Users/cns/.ssh/authorized_keys

$ ./scripts/seed-decoys.sh ./bin/sandbox-probe
seed-decoys: planted 17, skipped 9 (already present / unwritable)
```

17 fresh decoys planted (e.g. `~/.aws/credentials`, `~/.git-credentials`,
`~/.npmrc`, `~/.gcloud/credentials.db`); 9 skipped because a real file
already existed there (e.g. `~/.ssh/id_rsa`) — the soft-seed contract held,
nothing real was touched.

## Runs

```
$ export PROBE="$(pwd)/bin/sandbox-probe"
$ export OUT=.../report-off.json CLAUDE_SANDBOX=off RUNNER=research-manual
$ bash scripts/run-probe-via-claude-stub.sh
...
[mock-agent-api] anthropic Bash -> .../bin/sandbox-probe scan --tasksets baseline \
  --tags runner=research-manual,harness=claude,sandbox=off,claude=2.1.220,mode=via-claude-stub \
  --output_path .../report-off.json
claude(stub) wrote .../report-off.json

$ export OUT=.../report-on.json CLAUDE_SANDBOX=on
$ bash scripts/run-probe-via-claude-stub.sh
...
[mock-agent-api] anthropic Bash -> .../bin/sandbox-probe scan --tasksets baseline \
  --tags runner=research-manual,harness=claude,sandbox=on,claude=2.1.220,mode=via-claude-stub \
  --output_path .../report-on.json
claude(stub) wrote .../report-on.json
```

Both completed cleanly; the mock's `stub_finish`/EXIT trap tore down the
mock server each time (verified no leftover `mock-agent-api`/`sandbox-probe
scan`/`sandbox-exec` processes afterwards).

## Comparison

**`sensitive_readable_paths` — byte-for-byte identical, 33 entries in each,**
including every planted decoy (`~/.aws/credentials`, `~/.git-credentials`,
`~/.npmrc`, `~/.gcloud/credentials.db`, `~/.vault-token`, `~/.netrc`, …):

```
$ diff <(jq -r '.findings[]|select(.findingType=="sensitive_readable_paths")|.value[]' report-off.json | sort) \
       <(jq -r '.findings[]|select(.findingType=="sensitive_readable_paths")|.value[]' report-on.json  | sort)
$                      # no diff
```

**Identity of the process/host — identical, both runs:**

```
$ jq -c '.findings[]|select(.findingType=="hostname_detection")' report-off.json report-on.json
{"findingType":"hostname_detection",...,"value":"vigl.cns.me"}
{"findingType":"hostname_detection",...,"value":"vigl.cns.me"}

$ jq -c '.findings[]|select(.findingType=="user_context_detection").value.uid,.euid' report-off.json report-on.json
501   # same in both
```

Real hostname, real uid — this is the same process tree on the same
machine, not a container/VM guest with its own identity. `bin/sandbox-probe`
was never copied into a fresh rootfs; it just ran as a Seatbelt-confined
child of the same `claude` process.

**The sandbox is genuinely active, not a no-op** — network egress differs:

```
$ jq -c '.findings[]|select(.findingType=="external_host_connectivity")' report-off.json report-on.json
{"findingType":"external_host_connectivity",...,"value":["google.com"]}
{"findingType":"external_host_connectivity",...,"value":[]}
```

`CLAUDE_SANDBOX=off`: `google.com` reachable. `CLAUDE_SANDBOX=on`: nothing
reachable — Seatbelt's default network-deny is doing real work.

**`sandbox_detection` fires only when on**, correctly fingerprinting the
engine:

```
$ jq -c '.findings[]|select(.findingType=="sandbox_detection")' report-on.json
{"findingType":"sandbox_detection","task":"baseline_sandbox_detector","description":"Container/wrapper runtime","value":"seatbelt"}
```

(absent entirely from `report-off.json`, as expected.)

## Why reads aren't blocked (and why that's not a nesting bug)

`scripts/config/claude-code-sandbox.json` only turns the sandbox on
(`enabled`, `failIfUnavailable`, `allowUnsandboxedCommands: false`) — it
doesn't add its own filesystem policy, so Claude Code's sandbox falls back
to its underlying engine's own defaults. On macOS that's the same
Seatbelt-based `srt` (`@anthropic-ai/sandbox-runtime`) behavior already
audited for `srt`/`firejail`/`nono`
([firejail-nono-srt-flag-audit.md](firejail-nono-srt-flag-audit.md)):
**read access allowed everywhere by default, write denied everywhere unless
explicitly allowed, network denied by default.** That's exactly the pattern
measured here — reads identical to baseline, writes empty in both runs
(inconclusive either way — file ownership prevented writes regardless of
sandbox), network cut off only when sandboxed. This is a real, faithfully-
vendor-default *policy* result, not evidence of disconnection — precisely
the outcome the canary model in `map.md` is designed to produce once
nesting is correct.

## Practical consequence

No change needed to `scripts/run-probe-via-claude-stub.sh` or
`scripts/config/claude-code-sandbox.json`. `claude-sandbox` joins
`srt`/`firejail`/`nono` as already-correctly-nested — restricting the same
filesystem via policy rather than swapping in a disconnected rootfs.
[Ticket 08](../../.scratch/sandbox-canary-nesting/issues/08-consolidate-nesting-design.md)'s
consolidated fix can treat it as out of scope for the core nesting bug, the
same way it already treats `srt`/`firejail`/`nono`. The one open item is
the Linux/bubblewrap half of this same script, which still needs a real
empirical check on a Linux machine — not assumed identical just because
Seatbelt checked out clean.
