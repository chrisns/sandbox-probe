# Research: is `codex-sandbox` genuinely nested in the seeded parent host?

Source ticket: `.scratch/sandbox-canary-nesting/issues/12-codex-sandbox-nesting.md`,
part of the [sandbox canary nesting map](../../.scratch/sandbox-canary-nesting/map.md).

## Question

`scripts/run-probe-via-codex-stub.sh` runs the real `codex` CLI with
`CODEX_SANDBOX=on` → `--sandbox workspace-write`, which is documented as
applying Codex's own sandbox (Seatbelt on macOS, bubblewrap+seccomp on Linux)
directly to the shell subprocess — no container/rootfs construction by the
script. This looks structurally like the already-confirmed-fine
`srt`/`firejail`/`nono` cases (same filesystem, policy-restricted) rather
than the broken 5-runtime cases (fresh, disconnected rootfs) — but that was
an assumption, not confirmed. Investigated empirically on macOS (Seatbelt
path): seed decoys with `scripts/seed-decoys.sh`, run the codex-stub script
with `CODEX_SANDBOX=on` vs `off`, compare whether the seeded parent's decoys
are reachable from inside Codex's own sandbox. Also checked whether
`workspace-write`'s specific semantics (write restricted to workspace, read
presumably broader) change what "reachable" means compared to a
generic deny-all sandbox.

## Answer, up front

**No fix needed — `codex-sandbox` is already genuinely nested in the seeded
parent host.** Codex applies Seatbelt directly to the shell subprocess in
the same process tree, same `$HOME`, same filesystem — not a fresh
container/rootfs. Confirmed empirically:

1. **Read reachability is identical confined vs. unconfined.** All 33
   `sensitive_readable_paths` findings (including real credentials already
   present at the seeded targets — `~/.ssh/id_rsa`, `~/.aws/credentials`,
   `~/.kube/config`, etc.) are byte-identical between `CODEX_SANDBOX=on` and
   `CODEX_SANDBOX=off` runs. `workspace-write` does not restrict reads —
   confirming the ticket's suspicion that `workspace-write`'s read/write
   asymmetry matters: this mode is not comparable to a deny-all sandbox on
   the read axis.

2. **Write confinement is real and correctly scoped**, verified directly
   (not via the probe's own `writeable_paths` task, which turned out not to
   exercise this boundary — see Side finding below). A write to
   `$HOME/.codex-sandbox-nesting-write-test` succeeded unconfined
   (`HOME_WRITE:0`) and was rejected under `--sandbox workspace-write`
   (`HOME_WRITE:1`, `Operation not permitted`), while a write to `/tmp`
   succeeded in both — matching Codex's own banner:
   `sandbox: workspace-write [workdir, /tmp, $TMPDIR]`. This is Seatbelt
   genuinely enforcing a boundary against the real host filesystem, not a
   disconnected rootfs silently containing nothing.

3. **The probe's own sandbox fingerprint fires correctly.** The
   `baseline_sandbox_detector` task (uses the real macOS
   `sandbox_check()` private API, `pkg/tasks/baseline/seatbelt_darwin.go`)
   reports `"sandbox_detection": "seatbelt"` only under `CODEX_SANDBOX=on`;
   the finding is absent entirely when unconfined. Independent confirmation
   Seatbelt is actually active on the probe's own process, not just claimed
   by Codex's banner.

4. **Network is fully blocked under `workspace-write`**, as documented:
   `external_host_dns_resolution` and `external_host_connectivity` are both
   empty confined, populated unconfined (resolved `google.com`, reached
   port 80/443). Not part of the nesting question, but relevant if `on`/`off`
   reports are compared for anything network-shaped.

**Side finding (not a nesting bug, but limits what this comparison can show
for writes):** the probe's `writeable_paths` finding
(`baseline_filesystem_enumerator` → `PathTask` → `ScanTargetedPaths` →
`SystemWritePaths` in `pkg/tasks/baseline/filesystem.go`) only tests
root-owned system directories (`/etc`, `/usr`, `/bin`, `/var`, ...), never
`$HOME` or `/tmp`. Those are correctly empty under both `on` and `off` here
— an unprivileged user can't write to them either way — so the probe's
existing `writeable_paths` output is silent on `workspace-write`'s actual
write boundary. The manual check above (item 2) fills that gap for this
investigation; it's not a nesting-fix item, just a note that this probe task
wouldn't have caught a nesting regression on the write side by itself.

## Method

macOS 26.5.2 (Darwin 25.5.0, arm64), `codex-cli 0.142.5`, `jq 1.8.2`,
`node v22.19.0`. This machine is Chris's real development laptop — decoys
were seeded only via the existing `scripts/seed-decoys.sh` (soft: never
overwrites an existing file). All 26 seedable targets already existed as
real files (`planted 0, skipped 26`), which serves the canary model exactly
as well: the test is whether the sandboxed process can reach what's already
there in the seeded parent, and these are real, already-present targets at
the same paths the probe's target registry seeds.

```
$ make build
$ ./scripts/seed-decoys.sh ./bin/sandbox-probe
seed-decoys: planted 0, skipped 26 (already present / unwritable)
```

Baseline (unconfined) and confined runs, per the script's documented
contract (`PROBE`, `OUT`, `CODEX_SANDBOX`):

```
$ PROBE="$(pwd)/bin/sandbox-probe" OUT=/tmp/codex-nesting-test/off.json \
    CODEX_SANDBOX=off ./scripts/run-probe-via-codex-stub.sh
...
sandbox: danger-full-access
...
codex(stub) wrote /tmp/codex-nesting-test/off.json

$ PROBE="$(pwd)/bin/sandbox-probe" OUT=/tmp/codex-nesting-test/on.json \
    CODEX_SANDBOX=on ./scripts/run-probe-via-codex-stub.sh
...
sandbox: workspace-write [workdir, /tmp, $TMPDIR]
...
codex(stub) wrote /tmp/codex-nesting-test/on.json
```

Read comparison:

```
$ jq -r '.findings[] | select(.findingType=="sensitive_readable_paths") | .value' off.json > off-readable.txt
$ jq -r '.findings[] | select(.findingType=="sensitive_readable_paths") | .value' on.json  > on-readable.txt
$ diff off-readable.txt on-readable.txt
$ # (no output — identical, 33 paths in both, including
$ #  /Users/cns/.ssh/id_rsa, ~/.aws/credentials, ~/.kube/config, ...)
```

Sandbox fingerprint:

```
$ jq -r '.findings[] | select(.task=="baseline_sandbox_detector")' on.json
{
  "findingType": "sandbox_detection",
  "task": "baseline_sandbox_detector",
  "description": "Container/wrapper runtime",
  "value": "seatbelt"
}
$ jq -r '.findings[] | select(.task=="baseline_sandbox_detector")' off.json
$ # (no output — no finding at all when unconfined)
```

Write confinement (direct check, since `writeable_paths` doesn't exercise
`$HOME`/`/tmp` — see Side finding): `PROBE_CMD` overridden to a small script
that writes to `/tmp` and to `$HOME`, run through the same stub plumbing so
it still executes inside Codex's real sandbox:

```
$ cat write-test.sh
#!/bin/bash
echo wrote > /tmp/codex-nesting-test/write-marker.txt; echo "TMP_WRITE:$?"
echo x > "$HOME/.codex-sandbox-nesting-write-test"; echo "HOME_WRITE:$?"
echo done > "$1"

$ PROBE_CMD="/tmp/codex-nesting-test/write-test.sh /tmp/codex-nesting-test/on.json" \
  PROBE=irrelevant OUT=/tmp/codex-nesting-test/on.json CODEX_SANDBOX=on \
    ./scripts/run-probe-via-codex-stub.sh
...
TMP_WRITE:0
HOME_WRITE:1
/tmp/codex-nesting-test/write-test.sh: line 4: /Users/cns/.codex-sandbox-nesting-write-test: Operation not permitted

$ PROBE_CMD="/tmp/codex-nesting-test/write-test.sh /tmp/codex-nesting-test/off.json" \
  PROBE=irrelevant OUT=/tmp/codex-nesting-test/off.json CODEX_SANDBOX=off \
    ./scripts/run-probe-via-codex-stub.sh
...
TMP_WRITE:0
HOME_WRITE:0
```

All scratch files (`/tmp/codex-nesting-test/`,
`$HOME/.codex-sandbox-nesting-write-test`) were removed after the run; no
decoys were planted beyond `seed-decoys.sh`'s own soft seeding (which
planted nothing new on this machine, since every target already existed).

## Not tested here

The Linux path (`bubblewrap+seccomp`, per the script's own comment) needs a
real Linux box or VM with `bwrap` installed — this machine is macOS only.
Given `srt`/`firejail`/`nono` (the sibling ticket covering bwrap-adjacent,
policy-restricting-not-rootfs-swapping tools) and the structural argument in
the script itself (`codex` wraps only the shell child, same as Seatbelt),
the Linux path is expected to nest the same way, but that's an inference
from the mechanism, not a separate empirical confirmation. Worth a quick
follow-up on a Linux runner if the map wants full parity across both
platforms before closing the harness-nesting question generally.

## Conclusion

No fix needed for `scripts/run-probe-via-codex-stub.sh` or
`scripts/run-probe-in-sandbox.sh`. Codex's own sandbox is applied in-process
to the shell subprocess, genuinely nested in the seeded parent host: reads
of seeded/real credentials are unaffected by `workspace-write` (by design —
this mode restricts writes, not reads), writes outside the permitted
workspace/`/tmp` scope are genuinely blocked by Seatbelt, and the probe's
own sandbox fingerprint correctly detects Seatbelt only when it's actually
active. This matches the map's `srt`/`firejail`/`nono` pattern, not the
five broken container/rootfs-swapping runtimes.
