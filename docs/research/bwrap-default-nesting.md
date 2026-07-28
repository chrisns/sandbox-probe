# Research: what's the bwrap analogue to "genuinely nested in the seeded parent"?

Source ticket: `.scratch/sandbox-canary-nesting/issues/03-bwrap-default-nesting.md`,
part of the [sandbox canary nesting map](../../.scratch/sandbox-canary-nesting/map.md).

## Question

`bwrap` has no vendor "default" the way Docker/Podman do — every namespace and
bind decision is an explicit flag. The current script
(`scripts/run-probe-in-sandbox.sh`, `bwrap)` case) reconstructs a minimal root
(`--ro-bind /usr /usr --ro-bind /bin /bin ... --bind "$PWD" /work`), binding
no `/home` at all, so nothing seeded on the parent is reachable. Investigated
empirically:

1. Does `bwrap --bind / / --unshare-user --unshare-ipc --unshare-uts
   --unshare-cgroup --die-with-parent <cmd>` still fingerprint as bubblewrap
   while genuinely nesting in the seeded parent?
2. Does bwrap's own docs suggest a "typical" invocation closer to a sensible
   default than either the current minimal reconstruction or full
   `--bind / /`?
3. Does full-root-bind strip out isolation bwrap is supposed to provide,
   beyond just filesystem visibility?

## Answer, up front

1. **Yes, unconditionally, and for a reason that has nothing to do with the
   filesystem.** `sandbox-probe`'s bwrap fingerprint
   (`pkg/tasks/baseline.go` → `GetBubbleWrap()` in
   `pkg/tasks/baseline/environment.go`) is a pure **ancestor-process-name
   walk** — it checks whether any parent in `/proc` has `bwrap` in its
   command line. It does not look at any mount, bind, or namespace content.
   Since neither the current script nor `--bind / /` ever pass
   `--unshare-pid` (so `bwrap` stays visible as a real parent process) or
   otherwise hide the ancestor, **the filesystem topology is irrelevant to
   detection** — full-root-bind fingerprints exactly as reliably as the
   minimal reconstruction. Verified empirically below: same
   `"sandbox_detection": "bubblewrap"` finding either way, and a canary
   planted at a real path on the parent (`~/.ssh/id_rsa_canary`) is
   unreachable under the current minimal script and fully read/write
   reachable — including a round-trip write that survives the sandbox
   exiting — under `--bind / /`.

2. **bwrap's own README example is itself a minimal reconstruction, not a
   full-root bind** — closer to the current script's approach than to
   `--bind / /`. Bubblewrap's design philosophy (from its own README) is
   explicit that it has no default policy at all: *"bubblewrap is not a
   complete, ready-made sandbox with a specific security policy... the level
   of protection between the sandboxed processes and the host system is
   entirely determined by the arguments passed to bubblewrap"* — confirming
   the ticket's framing that there's no vendor default to defer to here, the
   way there is for Docker/Podman. But the one illustrative example the
   README does give is:
   ```
   bwrap \
       --ro-bind /usr /usr \
       --symlink usr/lib64 /lib64 \
       --proc /proc \
       --dev /dev \
       --unshare-pid \
       --new-session \
       bash
   ```
   — a curated, mostly-read-only root, explicitly called "incomplete... but
   useful for purposes of illustration." So even bwrap's own documentation
   leans toward "construct a minimal root," not "bind everything." This
   *does* resolve differently from the container-runtime tickets (there's
   genuinely no default to adopt), but the closest thing bwrap's own docs
   offer to a "typical" shape is closer in kind to the current script than
   to full-root-bind.

3. **Yes — full `--bind / /` silently drops bwrap's write confinement, not
   just its read/visibility scope, and that's a real regression, not merely
   "more filesystem visible."** The current minimal script uses `--ro-bind`
   for `/usr /bin /sbin /lib /lib64 /etc` — those are mount-level read-only,
   enforced by bwrap regardless of the calling user's actual Unix
   permissions on those paths. `--bind / /` is a plain (read-write) bind of
   the entire host root. Verified empirically below: under `--bind / /`, a
   write to `/etc` was blocked only by the *ordinary Unix permission check*
   (`devuser` doesn't own `/etc`) — not by any bwrap-level enforcement —
   while the same write attempt under the minimal reconstruction failed with
   `Read-only file system`, a namespace-level guarantee independent of who's
   running it. Concretely: a caller who *does* own/have write access to some
   host path outside their home directory (a shared group-writable dir, a
   world-writable `/tmp` subtree, etc.) would have that access preserved
   under `--bind / /` in a way the minimal reconstruction's `--ro-bind`
   would have blocked outright. This conflates "what a real bwrap deployment
   would confine" with "what the underlying Unix DAC happens to allow" —
   real bwrap consumers (Flatpak, Firejail-via-bwrap, etc.) essentially
   always keep the base OS tree read-only and bind only specific writable
   paths, so `--bind / /` tests a materially weaker, non-representative
   configuration, not just a more permissive filesystem view.

## Method

Docker Desktop's `desktop-linux` VM (`linux/arm64`, kernel
`6.12.76-linuxkit`, disposable) was used, per the map's suggested test
environment. An `ubuntu:24.04` container (`--privileged`, to allow
unprivileged user namespace creation) had `bubblewrap` installed from the
distro package (`bwrap --version` → `0.9.0`), plus a non-root user
(`devuser`, uid 1500) — matching the real-world case (CI runner, or a
developer's own laptop session), since testing as root inside `docker exec`
makes `bwrap`'s uid-remap trivial/no-op and isn't representative.

`sandbox-probe` was cross-compiled for `linux/arm64` and copied into the
container:

```
$ GOOS=linux GOARCH=arm64 go build -o sandbox-probe-linux-arm64 .
$ docker cp sandbox-probe-linux-arm64 bwrap-nest:/usr/local/bin/sandbox-probe
```

A canary was planted at a real path on the parent, before invoking bwrap —
matching the map's canary model (seed on the parent, launch bwrap as a real
child of it, test reachability from inside):

```
$ docker exec -u devuser bwrap-nest bash -c \
  'mkdir -p ~/.ssh && echo "decoy-canary-content" > ~/.ssh/id_rsa_canary'
```

## Detection logic read first

`pkg/tasks/baseline.go`, `SandboxTask.Run()`:

```go
// Detect bwrap explicitly (uid-map / ancestor walk)
log.Info().Msg("Checking for bubblewrap")
isBwrap, _ := baselineTasks.GetBubbleWrap(os.Getpid())
if isBwrap {
    runtimeStr = "bubblewrap"
}
```

`pkg/tasks/baseline/environment.go`, `GetBubbleWrap()`:

```go
// GetBubbleWrap tries to detect if the system is running in bwrap
func GetBubbleWrap(pid int) (bool, error) {
	proc, err := getRunningParentProcessLinux(pid)
	if err != nil || proc == nil || proc.PID < 0 {
		return false, nil
	}
	log.Info().Msgf("found this parent command %s", proc.Command)
	if strings.Contains(proc.Command, "bwrap") {
		return true, nil
	}
	is_bwrap, err := GetBubbleWrap(proc.PID)
	...
```

Despite the comment ("uid-map / ancestor walk"), this specific code path —
the one actually wired into the `baseline_sandbox_detector` task and thus
into `scan-matrix.yaml`'s `expect: '["bubblewrap", ...]'` assertion — is
**pure ancestor-process-name matching**, recursively walking `/proc` parents
looking for the literal substring `bwrap` in a command line. (There is a
separate `isUserNamespaceWithUIDMap()` helper in the same file, used inside
the more general `DetectSandbox()` multi-mechanism classifier, that inspects
`/proc/self/uid_map`. It is not what `SandboxTask.Run()` calls for the
`sandbox_detection` finding, so filesystem/uid-map content doesn't factor
into the assertion this ticket cares about.) This means: **as long as `bwrap`
itself stays visible as a real parent process (i.e. no `--unshare-pid`), the
fingerprint is filesystem-invariant** — full-root-bind and minimal
reconstruction detect identically.

## Experiment 1: minimal reconstruction (current script) — canary unreachable

```
$ docker exec -u devuser -w /home/devuser bwrap-nest bash -c '
  bwrap --ro-bind /usr /usr --ro-bind /bin /bin --ro-bind-try /sbin /sbin \
    --ro-bind /lib /lib --ro-bind-try /lib64 /lib64 --ro-bind /etc /etc \
    --proc /proc --dev /dev --bind "$PWD" /work --chdir /work \
    --unshare-user --unshare-ipc --unshare-uts --unshare-cgroup --die-with-parent \
    /bin/sh -c "cat /home/devuser/.ssh/id_rsa_canary 2>&1 || echo NOT-REACHABLE; \
                ls /home 2>&1 || echo NO-HOME-DIR-AT-ALL"
'
cat: /home/devuser/.ssh/id_rsa_canary: No such file or directory
NOT-REACHABLE
ls: cannot access '/home': No such file or directory
NO-HOME-DIR-AT-ALL
```

Confirms the map's diagnosis: `/home` isn't bound at all, so the seeded
canary is categorically unreachable regardless of any policy question —
there was never a route to it. Running the actual probe binary through this
invocation still correctly fingerprints as bubblewrap (ancestor walk sees
`bwrap` as parent):

```
"sandbox_detection" / "Container/wrapper runtime": "bubblewrap"
"sandbox_detection" / "Active kernel enforcement mechanism": "no-new-privs"
```

## Experiment 2: `--bind / /` — canary reachable, still fingerprints as bwrap

```
$ docker exec -u devuser -w /home/devuser bwrap-nest bash -c '
  bwrap --bind / / --unshare-user --unshare-ipc --unshare-uts --unshare-cgroup \
    --die-with-parent /usr/local/bin/sandbox-probe scan --tasksets baseline \
    --output_path /home/devuser/full-report.json
  bwrap --bind / / --unshare-user --unshare-ipc --unshare-uts --unshare-cgroup \
    --die-with-parent cat /home/devuser/.ssh/id_rsa_canary
'
...
decoy-canary-content
```

The probe's own report (`full-report.json`) confirms detection held:

```json
{ "findingType": "sandbox_detection", "description": "Container/wrapper runtime", "value": "bubblewrap" },
{ "findingType": "sandbox_detection", "description": "Active kernel enforcement mechanism", "value": "no-new-privs" }
```

Round-trip check — proving this is genuine nesting (same bind-mounted inode)
and not a lookalike copy:

```
$ docker exec -u devuser -w /home/devuser bwrap-nest bash -c '
  bwrap --bind / / --unshare-user --unshare-ipc --unshare-uts --unshare-cgroup \
    --die-with-parent /bin/sh -c "echo MODIFIED-FROM-INSIDE-BWRAP >> /home/devuser/.ssh/id_rsa_canary"
  echo "--- exited bwrap, reading from the real parent ---"
  cat /home/devuser/.ssh/id_rsa_canary
'
--- exited bwrap, reading from the real parent ---
decoy-canary-content
MODIFIED-FROM-INSIDE-BWRAP
```

A write made from inside the bwrap sandbox is visible on the parent after
bwrap has exited — confirming this is a real bind mount into the seeded
parent's actual filesystem, not a disconnected snapshot.

## Experiment 3: full-root-bind's write confinement is gone, not just read visibility

```
$ docker exec -u devuser -w /home/devuser bwrap-nest bash -c '
  bwrap --bind / / --unshare-user --unshare-ipc --unshare-uts --unshare-cgroup \
    --die-with-parent /bin/sh -c "
      echo test-write > /home/devuser/canary-write-test 2>&1 && echo WROTE-TO-HOME-OK
      echo test-write > /tmp/bwrap-write-test 2>&1 && echo WROTE-TO-TMP-OK
      echo test-write > /etc/bwrap-write-test 2>&1 && echo WROTE-TO-ETC-OK || echo ETC-WRITE-FAILED
    "
'
WROTE-TO-HOME-OK
WROTE-TO-TMP-OK
ETC-WRITE-FAILED
```

`/etc` write failed here — but check *why*:

```
$ docker exec -u devuser -w /home/devuser bwrap-nest bash -c '
  bwrap --bind / / ... /bin/sh -c "echo x > /etc/bwrap-write-test"
'
/bin/sh: cannot create /etc/bwrap-write-test: Permission denied
```

vs. the minimal reconstruction's `--ro-bind /etc /etc`:

```
$ docker exec -u devuser -w /home/devuser bwrap-nest bash -c '
  bwrap --ro-bind /etc /etc ... /bin/sh -c "echo x > /etc/bwrap-write-test2"
'
/bin/sh: cannot create /etc/bwrap-write-test2: Read-only file system
```

`Permission denied` vs. `Read-only file system` is the tell: under
`--bind / /`, `/etc` is mounted **read-write**; the write only failed
because `devuser` doesn't own it at the ordinary Unix-permission level. A
caller with broader host write access (their own `$HOME` subtree — already
demonstrated writable above — or any group/world-writable path) keeps that
access under `--bind / /` in a way `--ro-bind` would have blocked
unconditionally. This also shows up as a side effect in the probe's own
`writeable_paths` finding: `["/dev"]` under the minimal reconstruction
(bwrap's own fresh, writable `--dev /dev` tmpfs) vs. `[]` under
`--bind / /` (the container's real `/dev`, whose writability follows real
host permissions instead).

## Recommendation

Given (1) full-root-bind fingerprints identically because detection is
ancestor-walk-based and filesystem-invariant, but (3) it silently drops
write confinement in a way that doesn't represent how bwrap is normally
deployed: prefer **`--ro-bind / /` plus targeted writable binds** (the
probe's own workdir/report path, `/tmp`) over unqualified `--bind / /`. That
preserves genuine nesting into the seeded parent (same read-only view of
`/home`, canaries reachable) while keeping bwrap's actual namespace-level
write guarantee intact, rather than silently degrading it to whatever the
underlying Unix permissions happen to allow. Spot-checked:

```
$ docker exec -u devuser -w /home/devuser bwrap-verify bash -c '
  mkdir -p /home/devuser/work
  bwrap --ro-bind / / --bind /home/devuser/work /home/devuser/work --proc /proc --dev /dev \
    --unshare-user --unshare-ipc --unshare-uts --unshare-cgroup --die-with-parent \
    /bin/sh -c "
      cat /home/devuser/.ssh/id_rsa_canary && echo READ-OK
      echo w > /home/devuser/work/f && echo WRITE-TO-WORK-OK
      echo w > /home/devuser/.ssh/id_rsa_canary 2>&1 || echo WRITE-TO-SSH-BLOCKED-AS-EXPECTED
    "
'
canary
READ-OK
WRITE-TO-WORK-OK
/bin/sh: cannot create /home/devuser/.ssh/id_rsa_canary: Read-only file system
WRITE-TO-SSH-BLOCKED-AS-EXPECTED
```

Canary read succeeds (genuine nesting), the explicit workdir stays writable,
and everything else reverts to bwrap's own `Read-only file system`
enforcement — not a DAC accident. This is a **research answer, not a code
change** — no changes were made to `scripts/run-probe-in-sandbox.sh` or any
other file as part of this ticket.
