# Research: Podman's default sharing behavior, and rootless UID mapping

Source ticket: `.scratch/sandbox-canary-nesting/issues/02-podman-default-nesting.md`

## Question

What does a bare `podman run` (zero explicit flags) share by default —
filesystem and network — and how does that compare to the sibling
[Docker finding](01-docker-default-nesting.md)? Podman is rootless by
default: does that change what's reachable, specifically whether
`/run/user/<uid>/...` paths map differently between host and container in
ways relevant to the sibling `seed-ipc-targets` map's socket seeding? And
can `podman cp` move the probe binary in / report out of a container
without a bind mount, matching `docker cp`?

## Answer, up front

1. **No filesystem sharing by default.** A bare `podman run` sees none of
   the host's files — same structural absence (`ENOENT`, not `EACCES`) as
   Docker. Confirmed with a canary file in a scratch temp dir.
2. **Rootless mode does not change *what* is shared by default** — a bare
   `podman run` still doesn't expose `/run/user/<uid>` or anything else
   from the host. It changes *how ownership resolves* once something IS
   explicitly bind-mounted: by default the container's root (UID 0) is
   mapped, via the user namespace, to the real host user that invoked
   `podman`, not to host root. Files owned by that host user therefore
   appear owned by "root" inside the container automatically — no
   `--userns=keep-id` needed to read them. This is a real difference from
   Docker's rootful default (container root = host root, UID 0↔0) and
   matters for the sibling socket-seeding effort if/when a decoy socket
   under `/run/user/<uid>/...` is bind-mounted in: permission checks on
   that socket resolve through this remapped identity, not a naive
   UID-number comparison.
3. **Yes, bridge networking by default** — a bare `podman run` gets a
   `podman`-network bridge interface with NAT and working DNS resolution
   to the internet, same posture as Docker's default bridge network (and
   unlike the current script's explicit `--network=none`).
4. **Yes, `podman cp` works with zero bind mounts**, into a
   created-but-not-started container and back out of an exited one —
   confirmed directly, mirroring `docker cp`.

## Test environment

Podman was not pre-installed; installed via Homebrew (`brew install
podman`, v6.0.2) and initialized as a rootless VM (`podman machine init &&
podman machine start`), backed by `quay.io/podman/machine-os:6.0`
(Fedora CoreOS, kernel `7.1.3-200.fc44.aarch64`). All commands below ran
against `docker.io/library/ubuntu:latest`, invoked from macOS via the
`podman` CLI (which talks to the machine's rootless daemon over its API
socket) — the same relationship Docker Desktop has to its `desktop-linux`
VM in the sibling Docker research.

One environment wrinkle, noted for reproducibility, not relevant to the
findings: the ambient `~/.docker/config.json` sets `credsStore: "desktop"`
and gcloud `credHelpers`, which Podman also reads and tried to use for
plain `docker.io` pulls, failing with an unrelated gcloud auth error. Fixed
by pointing Podman at an empty auth file: `export
REGISTRY_AUTH_FILE=/tmp/empty-auth.json` (containing `{}`).

## 1. Filesystem: bare `podman run` shares nothing

```
$ mkdir -p /tmp/podman-scratch-test
$ echo "host-canary-content" > /tmp/podman-scratch-test/canary.txt

$ podman run --rm docker.io/library/ubuntu:latest cat /tmp/podman-scratch-test/canary.txt
cat: /tmp/podman-scratch-test/canary.txt: No such file or directory
exit=1

$ podman run --rm docker.io/library/ubuntu:latest ls -la /tmp
total 0
drwxrwxrwt+  2 root root  6 Jul 13 16:24 .
dr-xr-xr-x+  1 root root 28 Jul 28 14:46 ..
```

`/tmp` inside the container is a fresh, empty directory — the canary path
never existed, exactly the structural-absence signature already documented
for Docker (`docs/research/namespace-parity-semantics.md`): a private mount
namespace populated only with the image's own layers, nothing from the
host unless explicitly bind-mounted (`-v`/`--mount`).

`/home`, `/root`, and any Mac-side path (`/Users`) are equally absent —
confirmed with `ls -la /; ls -la /home; ls -la /Users` inside the same bare
container, none showing anything from the seeded parent.

## 2. Rootless mode: default sharing is unchanged; UID mapping is the real difference

### `/run/user/<uid>` is not auto-shared

```
$ podman run --rm docker.io/library/ubuntu:latest sh -c 'ls -la /run/; ls -la /run/user/ 2>&1; id'
total 0
drwxr-xr-x+  1 root root 42 Jul 28 14:48 .
-rw-r--r--+  1 root root  0 Jul 28 14:48 .containerenv
drwxrwxrwt+  2 root root  6 Jul 13 16:23 lock
drwxr-xr-x+  3 root root 60 Jul 28 14:48 secrets
drwxr-xr-x+  2 root root 23 Jul 13 16:25 systemd
ls: cannot access '/run/user/': No such file or directory
uid=0(root) gid=0(root) groups=0(root)
```

Rootless-ness doesn't leak the host's own `/run/user/<uid>` (where the
Podman machine's own `podman.sock` lives) into the container — same
"nothing shared unless mounted" rule as point 1. (The one Podman-specific
artifact: every container gets a `/run/.containerenv` marker file that
Docker containers don't have — a usable fingerprint, not a sharing path.)

### But UID mapping changes what happens once something IS shared

This is the part that matters for the sibling `seed-ipc-targets` socket
work. Seeded a real file as the *actual host user* (`core`, uid 501 — the
Podman machine VM's login user, standing in for a real Linux host's
invoking user) and bind-mounted it in:

```
$ podman machine ssh podman-machine-default \
    'mkdir -p /run/user/501/probe-test && echo host-secret > /run/user/501/probe-test/file.txt'

$ podman run --rm --security-opt label=disable \
    -v /run/user/501/probe-test:/mnt/test:ro docker.io/library/ubuntu:latest \
    sh -c 'ls -la /mnt/test; cat /mnt/test/file.txt; id'
drwxr-xr-x+ 2 root root 60 ...
-rw-r--r--+ 1 root root 12 ... file.txt
host-secret
uid=0(root) gid=0(root) groups=0(root)
```

(`--security-opt label=disable` only routes around the Podman machine
image's own SELinux enforcement — confirmed as the sole blocker via
`getenforce` → `Enforcing` and a re-run with `--userns=keep-id
--security-opt label=disable` succeeding with matching numeric
ownership. SELinux labeling is a property of this particular VM image, not
of rootless Podman generally, and orthogonal to the UID-mapping question.)

A file owned by host UID 501 shows up owned by **root** inside the
container, and the container reads it with no extra flags. `/proc/self/uid_map`
explains why:

```
$ podman run --rm docker.io/library/ubuntu:latest cat /proc/self/uid_map
         0        501          1
         1     100000    1000000
```

Container UID 0 is mapped 1:1 to host UID 501 — the real user that invoked
`podman`. Container UIDs 1+ map to the unprivileged subuid block
(`/etc/subuid`: `core:100000:1000000`). So "root" inside a rootless Podman
container is never real host root — it *is* the invoking host user,
wearing a UID-0 costume — and that identity is exactly what a
bind-mounted, host-user-owned file's permission check resolves against.
Contrast Docker's rootful default, where container UID 0 maps directly to
host UID 0 (real root) unless `userns-remap` is configured.

Practical read for the sibling ticket: if a decoy Unix socket seeded under
a real `/run/user/<uid>/...` path is bind-mounted into a rootless Podman
container (the only way it becomes reachable at all — see point 1), the
socket's connect-permission check will resolve through this namespace
mapping, not a literal host-UID-number match. A seeding/verification
script that assumes "container UID N == host UID N" will be wrong for the
default (non-`keep-id`) case; it should instead assume "container UID 0 ==
whatever host user ran podman."

## 3. Networking: bare `podman run` gets bridge + NAT + DNS, same posture as Docker

```
$ podman run --rm docker.io/library/ubuntu:latest sh -c 'python3 -c "import socket; print(socket.gethostbyname(\"example.com\"))"'
172.66.147.243  example.com   # (apt/python resolution output, abbreviated)

$ podman create --name nettest docker.io/library/ubuntu:latest sleep 300
$ podman inspect nettest --format 'NetworkMode: {{.HostConfig.NetworkMode}}'
NetworkMode: bridge
$ podman start nettest
$ podman inspect nettest --format '{{json .NetworkSettings}}'
{ "Gateway": "10.88.0.1", "IPAddress": "10.88.0.8", ...,
  "Networks": { "podman": { "Gateway": "10.88.0.1", "IPAddress": "10.88.0.8", ... } } }
$ podman exec nettest cat /etc/resolv.conf
nameserver 169.254.1.1
nameserver 192.168.127.1
```

Default `NetworkMode` is `bridge` on the `podman` network (`10.88.0.0/16`),
with working DNS and outbound internet access — the rootless equivalent of
Docker's default bridge (Docker uses a kernel bridge device + iptables NAT;
rootless Podman uses `slirp4netns`/`pasta`-style userspace networking to
reach the same effective default: unrestricted outbound, NAT'd, no inbound
without explicit `-p`). The current script's `--network=none` is, exactly
as in the Docker case, the harness's own addition, not Podman's default.

## 4. `podman cp` works with zero bind mounts, in and out

```
$ podman create --name cptest docker.io/library/ubuntu:latest /tmp/fake-probe.sh
$ podman ps -a --filter name=cptest
CONTAINER ID  IMAGE                            COMMAND    STATUS
7489c48f894c  docker.io/library/ubuntu:latest  ...        Created      # not started

$ podman cp /tmp/fake-probe.sh cptest:/tmp/fake-probe.sh
$ podman start -a cptest
fake probe binary output

$ podman cp cptest:/tmp/report.json /tmp/podman-scratch-test/report-out.json
$ cat /tmp/podman-scratch-test/report-out.json
probe ran, pid=1, uid=0
```

Copy-in worked on a container that was `create`d but never `start`ed (no
running process yet), and copy-out worked after it exited — identical
capability and identical two-step (`create`/`cp`/`start`) shape to
`docker cp`, with no `-v` anywhere in the sequence. This is a clean way to
get the probe binary in and `report.json` back out without adding any
filesystem sharing beyond Podman's actual default (none).

## Practical consequence for `scripts/run-probe-in-sandbox.sh`

The `podman)` case's current `--network=none -v "$PWD:/work"` is, like the
Docker case, entirely the harness's own addition — a bare `podman run`
starts with no filesystem sharing and full bridge networking, the inverse
of what's configured today. To genuinely nest the probe as a child of the
seeded parent while keeping the binary-in/report-out plumbing from adding
extra grants beyond Podman's default:

- Drop `--network=none` (or keep it deliberately and document that it's a
  harness restriction, not vendor default — same open question as
  [ticket 07](../../.scratch/sandbox-canary-nesting/issues/07-scope-decision-agent-harnesses.md)
  flags for Docker).
- Replace the `-v "$PWD:/work"` bind mount with `podman create` +
  `podman cp ... in` + `podman start` + `podman cp ... out`, so the only
  filesystem "sharing" is the explicit binary/report copy, not a live
  mount of the whole working directory.
- If/when the sibling `seed-ipc-targets` socket decoys land and need to be
  reachable from inside a rootless-Podman-sandboxed run, remember the
  UID-mapping finding above: reachability requires an explicit bind mount
  (nothing is implicit), and once mounted, permission resolution follows
  the user-namespace mapping (container UID 0 = invoking host user by
  default), not literal UID-number equality.
