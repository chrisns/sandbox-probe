# Research: does gVisor have a "default" host-sharing posture?

Source ticket: `.scratch/sandbox-canary-nesting/issues/05-gvisor-default-nesting.md`

## Question

`scripts/run-probe-in-sandbox.sh`'s `gvisor)` case runs `runsc` against a
fresh `docker export` of `ubuntu:latest`, disconnected from the seeded
parent host — the workflow's config-generation step already adds one bind
mount (`.mounts += [{"destination":$out,...}]`) purely to get the report
file out. Does gVisor/`runsc` have any notion of a "default" mount/sharing
posture the way Docker has default bridge networking, or is genuinely
everything explicit via the OCI spec's `mounts` array? What would adding the
seeded parent's paths to that same array look like, and is that the
vendor-intended mechanism? Empirically confirm reachability and that the
gVisor fingerprint still fires.

## Answer, up front

**No implicit default. Genuinely 100% explicit via the OCI `mounts` array —
there is nothing in gVisor analogous to Docker's default bridge network.**
`runsc spec`'s own generated template (gVisor's own tool, reproduced below)
contains exactly three mount entries — `/proc`, `/dev` (tmpfs), `/sys` — none
of which touch the host filesystem. No host directory, including `$HOME`, is
ever visible inside the sandbox unless a `mounts` entry says so. This was
confirmed both from gVisor's own docs and empirically: a bare `runsc run`
against the project's existing `ROOTFS` shows no trace of a decoy planted on
the "parent," and adding one more entry to the same `mounts` array — the
exact pattern the workflow already uses for the output-dir mount — makes
that decoy fully readable inside the sandbox, with the gVisor fingerprint
(`sandbox_detection: "gvisor"`) still firing correctly.

## 1. Is there a gVisor-side default, or is it all OCI-spec explicit?

All explicit. Evidence, primary-source first:

- **gVisor's filesystem docs**
  (<https://gvisor.dev/docs/user_guide/filesystem/>): "gVisor accesses the
  filesystem through a file proxy, called the Gofer. The gofer runs as a
  separate process, that is isolated from the sandbox." All host filesystem
  access is mediated by this gofer, and the gofer only serves paths it was
  configured with — the container's root (from the OCI bundle) plus whatever
  is listed in the spec's `mounts` array. There is no separate "share some
  host paths by default" behavior anywhere in that mediation layer.
- **gVisor's OCI quick-start**
  (<https://gvisor.dev/docs/user_guide/quick_start/oci/>): the canonical
  flow is `runsc spec -- /hello` then `sudo runsc run hello` — no bind mount
  is shown or implied as part of the standard workflow; the doc's own
  example never touches a host path beyond the bundle's own rootfs.
- **OCI runtime-spec** itself
  (<https://github.com/opencontainers/runtime-spec/blob/main/config.md#mounts>):
  `mounts` is explicitly OPTIONAL and the runtime "MUST mount entries in the
  listed order" — it's an ordered, opt-in list with no vendor-default
  contents; anything an OCI runtime (gVisor included) adds beyond what's in
  that array would be a runtime-specific extension, and gVisor doesn't add
  one for host paths.
- **Empirical**: `runsc spec`'s freshly generated `config.json` (runsc
  `release-20260721.0`, OCI spec `1.2.1`, arm64) is reproduced verbatim
  below — three mounts, zero of them host-facing:

  ```json
  "mounts": [
    { "destination": "/proc", "type": "proc",   "source": "proc" },
    { "destination": "/dev",  "type": "tmpfs",  "source": "tmpfs" },
    { "destination": "/sys",  "type": "sysfs",  "source": "sysfs",
      "options": ["nosuid", "noexec", "nodev", "ro"] }
  ]
  ```

  These are pseudo-filesystems runsc itself provisions for any Linux
  container to function (`/proc`, a `tmpfs` `/dev`, a read-only `/sys`) —
  not host shares. `/dev` is a fresh `tmpfs`, not a bind mount of the host's
  `/dev`. Confirmed by running the actual probe against this unmodified
  spec (see §3): the report shows zero decoys, zero mounted volumes, and a
  hostname of `runsc` — a genuinely disconnected sandbox, matching the
  ticket's premise about today's behavior exactly.

So "vendor default" here does mean what the ticket suspected: "whatever a
typical `runsc run` invocation looks like in gVisor's own docs/examples" —
and that typical invocation shares nothing beyond pseudo-filesystems. This is
architecturally unlike Docker, which auto-creates and auto-attaches a
default bridge network with no explicit opt-in required; gVisor has no
filesystem (or network) analogue of that implicit behavior.

## 2. What would sharing the seeded parent's paths look like?

Exactly the pattern the workflow's `jq` step already uses for the output
directory — one more object appended to the same `.mounts` array:

```
jq --arg root "$ROOTFS" --arg out "$OUTDIR" --arg home "$HOME" \
  '.root.path=$root | .process.args=$args | .process.terminal=false
   | .mounts += [{"destination":$out,"source":$out,"type":"bind","options":["bind","rw"]}]
   | .mounts += [{"destination":$home,"source":$home,"type":"bind","options":["bind","ro"]}]' \
  "$BUNDLE/config.json" > "$TMPCFG" && mv "$TMPCFG" "$BUNDLE/config.json"
```

Notes, all confirmed empirically in §3:

- **Same source and destination path** (`$HOME` -> `$HOME`) keeps the
  sandboxed probe's own path logic working unmodified — `os.UserHomeDir()`
  inside the sandbox resolves to the same path the seeder wrote to on the
  parent, so `pkg/tasks/baseline/filesystem.go`'s `sensitivePaths` (e.g.
  `h("/.ssh/id_rsa")`) line up without any translation.
- **Read-only (`"ro"`) is the right default** for decoy-reachability
  testing — it exercises exactly what `sensitive_readable_paths` measures
  without giving the sandboxed run a way to mutate parent-seeded state that
  later baseline runs depend on. Switch to `"rw"` only for a mount
  specifically used to probe `writeable_paths`.
- **No pre-existing destination directory is required.** The generated spec
  sets `root.readonly: true` and the fresh `ROOTFS` has no `/root/.ssh` at
  all, yet the bind mount worked with no error and no extra `mkdir -p` step
  — `runsc` (via the gofer) creates the mount target itself.
- This **is** the vendor-intended mechanism, not a workaround: gVisor's own
  Docker integration implements `docker run -v host:container` the same
  way, translating it into exactly this kind of OCI `mounts` entry under
  the hood. There is no alternative "shared folder" primitive in gVisor's
  docs for getting host state into a sandbox — bind mounts via `mounts` are
  the one mechanism, for containers and gofer-mediated paths alike.
- One asymmetry worth flagging for whoever wires this in for real: sharing
  is per-path, not per-user-tree. If the seeded parent's decoys live under
  several unrelated paths (not just `$HOME`), each needs its own `mounts`
  entry (or a common ancestor directory needs to be shared), same as any
  other OCI runtime.

## 3. Empirical verification

**Privilege/platform check, per the ticket's requirement:** this session
runs on macOS (no Linux kernel, no `runsc`, no root) — direct empirical
testing on the host was not possible. Docker Desktop's `desktop-linux` VM
(Linux 6.12.76-linuxkit, arm64) was used as the disposable Linux stand-in,
same approach the sibling `namespace-parity` research used. Inside that
VM's containers: root was available (`uid=0`), `/dev/kvm` was absent
(confirmed no KVM), and `runsc` installed cleanly from gVisor's official
apt repo (`storage.googleapis.com/gvisor/releases`, `release-20260721.0`).
Nested cgroup setup under Docker Desktop's own cgroup tree failed
(`cannot set up cgroup for root: ... device or resource busy`); resolved
with runsc's `--ignore-cgroups` flag, which is a nesting-environment
accommodation, not something that touches the mount/fingerprint behavior
under test. **Root + systrap were satisfied** (no KVM path was exercised or
needed) — the platform/privilege constraint the ticket named.

### Setup

A privileged container stood in for "the parent host" — same `ubuntu:latest`
`docker export` the real workflow uses for `ROOTFS`, with the (statically
linked, so no libc dependency) `sandbox-probe` binary copied in as `/probe`.
A decoy was seeded on that "parent," at exactly the path
`pkg/tasks/baseline/filesystem.go`'s fixed sensitive-path list already
checks (`/root/.ssh/id_rsa` — no `$HOME` expansion needed since the sandbox
runs as root):

```
$ cat /root/.ssh/id_rsa
-----BEGIN OPENSSH PRIVATE KEY----- DECOY-SEEDED-PARENT-CONTENT-nesting-test -----END OPENSSH PRIVATE KEY-----
```

### Run A — today's behavior: `runsc spec` output, only the output-dir mount added (no change from current script)

```
$ runsc --ignore-cgroups --network=none run -bundle /bundle-baseline gvisor-probe-baseline
```

Report excerpt:

```json
"sensitive_readable_paths": [
  "/etc/passwd", "/etc/shadow", ... , "/root"
],
"mounted_volumes_detections": [],
"sandbox_detection": "gvisor"
```

No `.ssh` anywhere — the fresh `ROOTFS` has no relationship to the seeded
parent, confirming the map's premise. Fingerprint already correct even in
this disconnected state.

### Run B — one more `mounts` entry, same pattern as the output-dir mount, sharing `/root/.ssh` from the "parent" into the sandbox

```
"mounts": [
  ...,
  {"destination":"/out","source":"/out","type":"bind","options":["bind","rw"]},
  {"destination":"/root/.ssh","source":"/root/.ssh","type":"bind","options":["bind","ro"]}
]
```

```
$ runsc --ignore-cgroups --network=none run -bundle /bundle-nested gvisor-probe-nested
```

Report excerpt:

```json
"sensitive_readable_paths": [
  "/etc/passwd", "/etc/shadow", ... , "/root",
  "/root/.ssh",
  "/root/.ssh/id_rsa"
],
"sandbox_detection": "gvisor"
```

The seeded decoy is now visible to the sandboxed probe, and only that one
path changed — everything else in the report (sockets, mounted-volume
detection, process/user context) is identical to Run A. Direct byte-level
confirmation, bypassing the probe entirely:

```
$ runsc --ignore-cgroups --network=none run -bundle /bundle-cat gvisor-cat
-----BEGIN OPENSSH PRIVATE KEY----- DECOY-SEEDED-PARENT-CONTENT-nesting-test -----END OPENSSH PRIVATE KEY-----
```

Byte-for-byte the same content written on the "parent" — a live, correct
bind mount, not just a stat-able placeholder.

### Fingerprint check, and the `/__runsc_containers__` marker specifically

`pkg/tasks/baseline/environment.go` checks `/__runsc_containers__` first,
then falls back to a `"gvisor"` substring match on `/proc/version`. Direct
check inside a bare `runsc run` container:

```
$ cat /__runsc_containers__
cat: /__runsc_containers__: No such file or directory
$ cat /proc/version
Linux version 4.19.0-gvisor #1 SMP Sun Jan 10 15:06:54 PST 2016
```

`/__runsc_containers__` genuinely does not exist under a plain `runsc run`
invocation — confirming `environment.go`'s own comment ("its synthetic
`/proc/version` ... present under `runsc run`, which does not create
`/__runsc_containers__`"). Detection actually fires via the `/proc/version`
fallback (`"gvisor"` substring), which both reports above show working
correctly (`sandbox_detection: "gvisor"`) with or without the added home
mount. Side note for whoever next touches `run-probe-in-sandbox.sh`: its own
comment on the `gvisor)` case says "so `/__runsc_containers__` exists ->
\"gvisor\"" — that's the marker that does **not** exist in this invocation
mode; the fingerprint actually depends on the `/proc/version` fallback, per
`environment.go`'s comment and confirmed here. Worth a one-line comment fix
whenever that script is next edited, independent of this ticket's mount
question.

### One gap surfaced, unrelated to reachability

`mounted_volumes_detections` stayed `[]` in both runs, even with the host
bind mount live and confirmed working via the other two checks.
`pkg/tasks/baseline`'s mount enumerator does not appear to surface gVisor's
virtualized mount table the way it does for other runtimes — a probe-side
gap, not a mounting-mechanism failure (the file was unambiguously reachable
and byte-correct by two independent checks). Flagging for whoever
implements the nesting fix in case that finding type matters for the
gVisor row's report.

## Practical consequence

For the ADR this ticket feeds: gVisor needs no special-casing beyond the
mechanism already proven in the workflow's own output-dir mount. Sharing the
seeded parent's `$HOME` (or whatever specific paths `seed-decoys.sh`
populates) is one more `.mounts += [...]` entry, `"ro"` by default, no
pre-created destination directory required, applied by the same `jq`
pipeline that already builds `config.json`. This is gVisor's
vendor-intended, only mechanism for host-state sharing — not a workaround —
and it doesn't disturb the gVisor fingerprint.
