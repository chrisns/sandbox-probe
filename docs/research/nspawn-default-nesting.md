# Research: can systemd-nspawn nest against the seeded parent host?

Source ticket: `.scratch/sandbox-canary-nesting/issues/04-nspawn-default-nesting.md`

## Question

`systemd-nspawn` currently runs against a `ROOTFS` the workflow builds fresh
from a `docker export` of `ubuntu:latest` — never the seeded parent. Does
nspawn have a mode that boots/nests against the host's own root (or a bind
of it) rather than requiring a separate prepared rootfs tree? If it
structurally requires a separate directory, what's nspawn's own documented
mechanism for sharing specific host paths in, and is `--bind=` sharing the
seeded parent's paths (e.g. `$HOME`) the vendor-intended way to do this, or
a workaround?

## Answer, up front

1. **No** — `systemd-nspawn -D /` is not just discouraged, it is a hard,
   built-in refusal. Confirmed empirically (exact error below) and consistent
   with the man page's model: nspawn takes over the *entire* filesystem
   hierarchy under `--directory=` (pivot_root-style, not a chroot overlay),
   so pointing it at the live host root would mean two kernels' worth of
   `/var`, cgroups, and mounts fighting over the same on-disk state.
2. **`--bind=`/`--bind-ro=` is the correct, vendor-intended mechanism**, not
   a workaround. The man page documents no other supported way to expose a
   host path inside an nspawn container's namespace, and the option exists
   for exactly this purpose ("Bind mount a file or directory from the host
   into the container").
3. **Verified empirically** with root, nested inside Docker Desktop's
   `desktop-linux` VM: a decoy file placed outside a `docker export`-built
   rootfs is unreachable by default, and reachable once shared via
   `--bind=`. The `container=systemd-nspawn` environment variable the probe
   already checks as a fallback fingerprint is set correctly in both cases,
   independent of the file-based `/run/systemd/container` marker.

## 1. Does nspawn support running directly against the host's own root?

The man page's `-D`/`--directory` option is plain about what it takes: "the
current directory will be used" if unset, and nspawn expects "any directory
tree containing an operating system tree" — its own guidance is to build one
with `dnf(8)`, `debootstrap(8)`, or `pacman(8)`, i.e. a *separate* prepared
tree, not "your live root." ([systemd-nspawn(1), man7.org][man7])

More decisively, nspawn refuses outright at runtime. Empirically, inside a
privileged container on Docker Desktop's `desktop-linux` VM (root, `systemd
259`):

```
# systemd-nspawn -q -D / /bin/echo "ran-against-live-root-ok"
Spawning container on root directory is not supported. Consider using
--ephemeral, --volatile=yes or --volatile=state.
```

This is a compiled-in check, not a permissions issue or an artifact of the
nested test environment — same message on every attempt. `--ephemeral`
(`-x`) and `--volatile=` are nspawn's escape hatches for this case, but both
change the semantics: they run against an **overlay/tmpfs snapshot** of the
given tree (throwaway, discarded on exit) rather than the tree itself, and
the man page's `--overlay=` documentation explicitly notes "this option
cannot be used to replace the root file system of the container with an
overlay file system" for the general case — reinforcing that nspawn's model
is "container root is a distinct tree," full stop.

The reason lines up with how nspawn works, per the man page's DESCRIPTION:
unlike `chroot`, it "virtualizes the file system hierarchy, as well as the
process tree, the various IPC subsystems, and the host and domain names" —
a full pivot_root-based takeover, not a bind overlay. Lennart Poettering
(systemd's author), on the systemd-devel mailing list, gives the concrete
hazard of trying to reuse a tree that's already live: "Linux systems are
generally not ready to be booted twice from the same writable `/var`... if
another instance modifies it behind its back it's going to be very
confused" — recommending `--read-only` for that case. The host's actual `/`
is always "already booted" by construction, so it is exactly the case this
warning describes. ([systemd-devel mailing list][ml])

**Conclusion: nspawn structurally requires a separate prepared rootfs
directory.** It is not a documentation gap or a missing flag — it is an
explicit, compiled-in refusal.

## 2. nspawn's own mechanism for sharing host paths: `--bind=`/`--bind-ro=`

Man page, verbatim: "Bind mount a file or directory from the host into the
container. Takes one of: a path argument — in which case the specified path
will be mounted from the host to the same path in the container, or a
colon-separated pair of paths — in which case the first specified path is
the source in the host, and the second path is the destination in the
container, or a colon-separated triple of source path, destination path and
mount options." `--bind-ro=` is the read-only variant; mount options
(`rbind`/`norbind`, `idmap`/`rootidmap`/`owneridmap` for UID mapping) are
comma-separated in the third field. ([systemd-nspawn(1), man7.org][man7])

There is no alternative documented mechanism for this — `--overlay=` builds
a merged filesystem from multiple directory trees but is explicitly not for
substituting the container root, and the whole point of `-D` requiring a
separate tree is that nothing propagates in automatically. `--bind=` is the
only vendor-supplied door.

**This is the intended way, not a workaround.** The existing script
(`scripts/run-probe-in-sandbox.sh`, `nspawn)` case) already uses exactly
this flag — `--bind="$(dirname "$OUT_ABS")"` — to get the report back out;
extending the same flag to also bind in the seeded parent's `$HOME` (or
whatever paths `seed-decoys.sh` seeded) is using the mechanism for what it's
documented to do, symmetrically for input as well as output.

## 3. Empirical verification (root available: nested Docker Desktop VM)

This machine is macOS — `systemd-nspawn` doesn't exist natively (Linux-only,
needs systemd). Root **was** available via a privileged container nested
inside Docker Desktop's `desktop-linux` VM (`ubuntu:26.04`, kernel
`6.12.76-linuxkit`), where `apt-get install systemd-container` provides a
real `systemd-nspawn 259`.

### Setup

Mirrors the workflow exactly: `docker export` of a throwaway `ubuntu:latest`
container into a `ROOTFS` dir, `.dockerenv` stripped. A `parent-home/`
directory *outside* that rootfs stands in for the seeded parent's `$HOME`,
containing a decoy file:

```
$ echo "canary-secret-12345" > parent-home/.ssh_decoy
```

### Result: default (no `--bind=`) — disconnected, as the map's diagnosis predicted

```
# systemd-nspawn -q --register=no --keep-unit -D rootfs \
    /bin/cat /root/parent-home/.ssh_decoy
cat: /root/parent-home/.ssh_decoy: No such file or directory
```

Confirms the map's core finding for this runtime: a `docker export`-built
`ROOTFS` with nothing bound in has no route to the seeded parent at all —
the existing baseline comparison for nspawn has been contentless.

### Result: with `--bind=` — reachable

```
# systemd-nspawn -q --register=no --keep-unit -D rootfs \
    --bind=parent-home:/root/parent-home \
    /bin/cat /root/parent-home/.ssh_decoy
canary-secret-12345
```

Also confirmed at a `$HOME`-shaped mount point (`--bind=parent-home:/home/parent`):
`ls -la /home/parent` shows the decoy, `cat` returns its contents.

### Result: fingerprint — `container=systemd-nspawn` set correctly

```
# systemd-nspawn -q --register=no --keep-unit -D rootfs \
    --bind=parent-home:/root/parent-home \
    /bin/sh -c 'echo container_env=$container'
container_env=systemd-nspawn
```

`pkg/tasks/baseline/environment.go`'s `GetContainerRuntime` checks two
independent signals for nspawn, either sufficient: the `/run/systemd/container`
file (line 239) and the `container` environment variable (line 248) — both
parsed by `stringToContainerRuntime`, which maps any value containing
`"nspawn"` to `RuntimeNspawn` (line 309). The env var was confirmed set
correctly above. `/run/systemd/container` was **not** populated in this
particular test run — see caveat below — but the code already treats the
two as equivalent, so this doesn't affect real-world detection.

### Caveat: `--register=no`/`--keep-unit` were needed here, and why

Nspawn's default behavior is to register the container with
`systemd-machined` over D-Bus and run its payload as a transient systemd
scope on the **host's** running systemd instance. On a real GitHub Actions
Linux runner, systemd is PID 1 and this "just works" with zero extra flags
— exactly what `scripts/run-probe-in-sandbox.sh` already does
(`sudo systemd-nspawn -q -D "$ROOTFS" --bind=... /probe ...`, no
`--register=no`).

This test environment is nested two levels deep (macOS → Docker Desktop's
Linux VM → a privileged container whose PID 1 is `bash`, not systemd), so
there was no host systemd to register with or allocate a scope from —
attempts without `--register=no` failed with `Failed to open system bus`
and then, once a D-Bus daemon was started by hand, `Failed to allocate
scope: Launch helper exited with unknown return code 1`. `--register=no
--keep-unit` skips both steps and runs the payload directly, which is a
property of the doubly-nested test harness, not of nspawn's real deployment
target. This is also almost certainly why `/run/systemd/container` (which
nspawn appears to populate as part of the normal registration path) was
absent while the `container` env var (set unconditionally, independent of
registration) was present — a genuinely useful finding for the probe's own
"check both" detection strategy, not a limitation to work around in the CI
workflow.

## Practical consequence for `scripts/run-probe-in-sandbox.sh`

The `nspawn)` case's `--bind="$(dirname "$OUT_ABS")"` for report output stays
as-is. To nest against the seeded parent, add the equivalent `--bind=` (or
`--bind-ro=`, if write access into the parent's `$HOME` shouldn't be granted)
for whatever `seed-decoys.sh` seeded on the host side — e.g.
`--bind-ro="$HOME"` alongside the existing output bind — using the exact same
flag, the exact same way, for the exact same reason. No other flag or mode
change is needed; `-D "$ROOTFS"` stays a separate prepared tree because
nspawn leaves no other option.

## Sources

- [`systemd-nspawn(1)` — man7.org][man7] (mirrors upstream systemd man
  pages): `-D`/`--directory`, `--bind=`/`--bind-ro=`, `--overlay=`,
  `-x`/`--ephemeral`, `--volatile=`, DESCRIPTION.
- [systemd-devel mailing list, Lennart Poettering, Sept 2018][ml]: hazard of
  booting/running against an already-live writable tree.
- Empirical: root shell nested inside Docker Desktop's `desktop-linux` VM
  (`ubuntu:26.04`, kernel `6.12.76-linuxkit`, `systemd-nspawn` 259 from
  `apt-get install systemd-container`), this session, 2026-07-28.
- `pkg/tasks/baseline/environment.go` (`GetContainerRuntime`,
  `stringToContainerRuntime`) and `environment_test.go` — existing nspawn
  fingerprint detection logic.

[man7]: https://man7.org/linux/man-pages/man1/systemd-nspawn.1.html
[ml]: https://lists.freedesktop.org/archives/systemd-devel/2018-September/041327.html
