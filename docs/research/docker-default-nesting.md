# Research: what does a bare `docker run` actually share by default?

Source ticket: `.scratch/sandbox-canary-nesting/issues/01-docker-default-nesting.md`
Map: `.scratch/sandbox-canary-nesting/map.md`

## Question

`scripts/run-probe-in-sandbox.sh`'s `docker)` case launches
`docker run --rm --network=none -v "$PWD:/work" -w /work ubuntu:latest ...` —
a fresh image with an explicit `--network=none` and a `$PWD`-only mount, no
relationship to the seeded parent's `$HOME` at all. Per the map, that makes
the "sandboxed run doesn't see the decoy" result meaningless: nothing was
ever shared, so of course nothing leaked. Before fixing the harness, four
things needed empirical answers rather than assumptions:

1. Does a bare `docker run <image> <cmd>` (zero `-v`/`--network`/etc flags)
   share anything from the host filesystem by default?
2. Does it share networking by default (Docker's actual default is bridge,
   unlike the script's explicit `--network=none`)?
3. Can `docker cp` move the probe binary in and `report.json` back out
   without adding any sharing beyond that default posture — including into
   a container that's been `docker create`d but not yet started, and out of
   one that has already exited?
4. Does "Docker's default" mean literally zero flags on bare Linux Docker,
   or does Docker Desktop (macOS/Windows) have different default
   host-sharing behavior worth distinguishing?

## Answers, up front

1. **No.** A bare `docker run` shares nothing from the host filesystem.
   Every container gets its own mount namespace populated only from the
   image layers; nothing propagates in without an explicit `-v`/`--mount`.
   Confirmed empirically below — a decoy created outside any container is
   invisible to a zero-flag container by every path tried.
2. **Yes.** Bridge networking (`NetworkMode=bridge`) is the actual default:
   the container gets an `eth0` on the `docker0`-equivalent bridge subnet,
   a default route, working DNS, and real outbound internet connectivity —
   none of which exist with the script's current explicit
   `--network=none`.
3. **Yes, both directions, before and after start.** `docker cp` talks to
   the daemon's container-filesystem API, not a bind mount — it works the
   instant a container exists (`docker create`, before `docker start`) and
   still works after the container has exited. `docker inspect` on such a
   container shows `Mounts=[]` and `Binds=[]` throughout — no filesystem
   sharing is ever added by using `cp` for binary-in/report-out.
4. **Distinguish them — they are not the same rig, though the container
   defaults measured here are Engine-level, not Desktop-specific.** This
   research ran on Docker Desktop for Mac (`desktop-linux` context), where
   the real Docker *daemon* host is a Linux VM one hop away from macOS —
   Docker Desktop's file-sharing allowlist only gates what an *explicit*
   `-v` from macOS is permitted to reach; it adds nothing to a zero-flag
   container, so it doesn't change answer 1. One genuine Desktop-specific
   difference did show up: DNS is proxied through Docker Desktop's internal
   resolver (`192.168.127.7`, a `gvisor-tap-vsock` address), not the classic
   Linux Engine's embedded DNS at `127.0.0.11`. CI (`scan-matrix.yaml`,
   `runs-on: ubuntu-latest`) runs bare Linux Docker Engine directly on the
   runner's own filesystem, with no VM hop — the actually-relevant target
   for this fix. The no-host-mount / bridge-by-default results are
   documented Engine-level behavior common to both; only the resolver
   address and the VM hop are Desktop artifacts, and this research could not
   independently re-verify against a real bare-Linux daemon (none available
   on this machine) — flagging rather than assuming.

## Environment

```
$ docker version
Client:
 Version:           29.6.1
 ...
 Context:           desktop-linux
Server: Docker Desktop 4.80.0 (232116)
 Engine:
  Version:          29.6.1
  ...
 OS/Arch:           linux/arm64
```

Everything below runs inside that VM's containers; nothing touched the real
Mac's `$HOME` or any real credentials. A scratch dir stood in for the "host":

```
$ SCRATCH=/private/tmp/.../scratchpad/docker-nesting-research
$ echo "aws_access_key_id = DECOY-NOT-REAL" > "$SCRATCH/parent-home/credentials"
```

## Experiment 1 — filesystem: bare `docker run` shares nothing

```
$ docker run --rm ubuntu:latest ls -la /
total 60
drwxr-xr-x   1 root root 4096 Jul 28 14:43 .
...
-rwxr-xr-x   1 root root    0 Jul 28 14:43 .dockerenv
drwxr-xr-x   3 root root 4096 Jul 13 16:25 home
...
```

Only the image's own rootfs — no `/work`, no scratch dir, no host path of
any kind mounted in. Confirmed authoritatively via the container's own mount
table, not just a directory listing (a recursive `grep -r /` was tried
first but never terminates — `/proc`/`/sys` contain effectively-infinite
pseudo-files — and was killed rather than left running):

```
$ timeout 20 docker run --rm ubuntu:latest cat /proc/mounts
overlay / overlay rw,relatime,lowerdir=.../snapshots/115/fs:.../snapshots/15/fs:.../snapshots/14/fs,upperdir=.../snapshots/116/fs,workdir=.../snapshots/116/work 0 0
proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
tmpfs /dev tmpfs rw,nosuid,size=65536k,mode=755 0 0
devpts /dev/pts devpts rw,nosuid,noexec,relatime,gid=5,mode=620,ptmxmode=666 0 0
sysfs /sys sysfs ro,nosuid,nodev,noexec,relatime 0 0
cgroup /sys/fs/cgroup cgroup2 ro,nosuid,nodev,noexec,relatime 0 0
mqueue /dev/mqueue mqueue rw,nosuid,nodev,noexec,relatime 0 0
shm /dev/shm tmpfs rw,nosuid,nodev,noexec,relatime,size=65536k 0 0
/dev/vda1 /etc/resolv.conf ext4 rw,relatime,discard 0 0
/dev/vda1 /etc/hostname ext4 rw,relatime,discard 0 0
/dev/vda1 /etc/hosts ext4 rw,relatime,discard 0 0
... (proc/sys sub-mounts, all pseudo-filesystem) ...
```

Every entry is either the image's own `overlay` root or a kernel
pseudo-filesystem (`proc`, `sysfs`, `tmpfs`, `devpts`, `mqueue`) — the only
real-disk entries (`/etc/resolv.conf`, `/etc/hostname`, `/etc/hosts`, all
`/dev/vda1`) are Docker's standard per-container network-identity files, not
anything host-provided. There is no bind mount of any host or scratch path
anywhere in the table — nothing to reach the decoy through, by construction,
not merely by absence of a lucky guess.

## Experiment 2 — network: bare `docker run` gets bridge, not none

```
$ docker run --rm ubuntu:latest sh -c "cat /proc/net/route; hostname -I; cat /etc/resolv.conf"
Iface   Destination  Gateway  Flags ...
eth0    00000000     010011AC 0003  ...   # default route via eth0
eth0    000011AC     00000000 0001  ...   # subnet route

172.17.0.6

nameserver 192.168.127.7
```

An `eth0` with a real IP on the `172.17.0.0/16` bridge subnet and a default
route — this is bridge networking, Docker's real default
(`docker network inspect bridge` confirms `Driver=bridge subnet=172.17.0.0/16`).
Actual reachability, contrasted directly against the script's current
`--network=none`:

```
$ docker run --rm curlimages/curl:latest -sS -m 5 -o /dev/null -w 'http_code=%{http_code} exit=%{exitcode}\n' https://1.1.1.1
http_code=301 exit=0

$ docker run --rm --network=none curlimages/curl:latest -sS -m 5 -o /dev/null -w 'http_code=%{http_code} exit=%{exitcode}\n' https://1.1.1.1
curl: (7) Failed to connect to 1.1.1.1:443 after 0 ms: Could not connect to server
http_code=000 exit=7
```

Zero flags: real outbound connectivity. `--network=none` (what the script
does today): connects to nothing, not even DNS. These are materially
different postures, and the script's is not Docker's default.

## Experiment 3 — `docker cp` in/out needs no bind mount, at any lifecycle stage

A stand-in "probe binary" (a shell script that writes a report):

```
$ printf '#!/bin/sh\necho "probe ran" > /out/report.json\ncat /etc/hostname >> /out/report.json\n' > "$SCRATCH/fake-probe.sh"
```

**Into a container that has been `create`d but never `start`ed:**

```
$ docker create ubuntu:latest sh -c "mkdir -p /out && /probe.sh"
06f0be91502b...
$ docker cp "$SCRATCH/fake-probe.sh" 06f0be91502b:/probe.sh
$ echo $?
0
$ docker cp 06f0be91502b:/probe.sh "$SCRATCH/roundtrip-probe.sh"   # read it straight back, still pre-start
$ diff "$SCRATCH/fake-probe.sh" "$SCRATCH/roundtrip-probe.sh" && echo "roundtrip identical"
roundtrip identical
```

`docker cp` succeeded in both directions before the container had ever run —
`docker create` alone is enough to allocate a container filesystem `cp` can
target.

**Full round trip — copy in, start, run to completion, copy the report back
out of the exited container:**

```
$ docker create ubuntu:latest sh -c "mkdir -p /out && /probe.sh"
f18183660c2b...
$ docker cp "$SCRATCH/fake-probe.sh" f18183660c2b:/probe.sh
$ docker start -a f18183660c2b
$ docker ps -a --filter id=f18183660c2b --format '{{.ID}} {{.Status}}'
f18183660c2b Exited (0) 4 seconds ago
$ docker cp f18183660c2b:/out/report.json "$SCRATCH/report-out.json"
$ cat "$SCRATCH/report-out.json"
probe ran
f18183660c2b
```

`docker cp` reads the report straight out of the exited container's
filesystem — no bind mount, no `--network`, no extra grant of any kind.

**Confirms no mount was ever involved**, on the exact container the copy
operations targeted:

```
$ docker create --name proofcheck ubuntu:latest true
$ docker inspect proofcheck --format 'Mounts={{json .Mounts}} NetworkMode={{.HostConfig.NetworkMode}} Binds={{.HostConfig.Binds}}'
Mounts=[] NetworkMode=bridge Binds=[]
```

`Mounts` and `Binds` are empty throughout — `docker cp` rides the daemon's
API (a tar stream over the Docker socket), entirely separate from the
container's mount table. Using it for binary-in/report-out adds nothing to
the container's default sharing posture; `NetworkMode` also confirms
`bridge` is what a plain `docker create`/`run` gets without asking.

## Practical consequence for `scripts/run-probe-in-sandbox.sh`

The current `docker)` (and `podman)`) case's `--network=none -v "$PWD:/work"`
is entirely the harness's own addition, not Docker's default, and it's
exactly backwards for the canary model in `map.md`: it removes the one thing
(network) Docker shares by default and adds a bind mount Docker does *not*
share by default — the opposite of "launch as a genuine child of the seeded
parent, nothing artificially opened or closed beyond the vendor default."

A default-faithful replacement plumbs the probe in/report out via
`docker create` → `docker cp` in → `docker start` → `docker cp` out →
`docker rm`, with **zero** `-v`/`--network`/`-w` flags — matching literally
what `docker run <image> <cmd>` gives you unprompted. That fixes the
network-off-by-default bug for free (bridge networking measures Docker's
real default egress exposure) and removes the meaningless `$PWD`-only mount
without opening any path to the seeded parent's `$HOME` that a bare
`docker run` wouldn't already have (i.e. none — filesystem isolation stays
exactly as tight as vendor default, per Experiment 1). This is a research
finding, not yet applied — per the map's "plan, don't do" standing
preference, the actual script edit is a follow-up decision (ADR territory),
not part of this ticket.
