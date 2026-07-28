# Research: does trae-agent's own `--docker-image` mode share `$HOME` by default?

Source ticket: `.scratch/sandbox-canary-nesting/issues/14-trae-docker-nesting.md`
Map: `.scratch/sandbox-canary-nesting/map.md`

## Question

`scripts/run-probe-via-trae-stub.sh` with `TRAE_DOCKER=on` re-execs the probe
via `trae-agent`'s (bytedance/trae-agent) own `--docker-image` mode, not a
container the harness constructs itself — the script's comment says this
"mounts `--working-dir`" into the container. Same shape of question as the
gemini-docker ticket: is this a genuine vendor default (legitimate "blocked"
result) or a disconnected-environment bug like the original five
`sandbox`-family runtimes? Three things needed answers:

1. What does trae-agent's `--docker-image` mode actually mount, read from
   its own source rather than inferred from the script comment — does it
   touch `$HOME`, or only the working directory?
2. Empirically: seed decoys in `$HOME`, run with `TRAE_DOCKER=on`, check
   reachability from inside trae's own container.
3. If not reachable, is that a genuine vendor default (what any real
   trae-agent docker-mode user gets) or an artifact of how the harness
   invokes it — and is there a vendor-exposed extension point (analogous to
   gemini-cli's sandbox flags) worth wiring up, without misrepresenting
   trae's real default for everyone else who uses it?

## Answers, up front

1. **Only the working directory. Never `$HOME`, and there is no flag, env
   var, or config field to add more.** Read directly from trae-agent's own
   installed source (`DockerManager.start()` in
   `trae_agent/agent/docker_manager.py`): when `--docker-image` is used with
   `--working-dir`, exactly one bind mount is constructed —
   `{workspace_dir: {bind: "/workspace", mode: "rw"}}` — and nothing else.
   No `$HOME`, no SSH/cloud-credential directories, no second mount of any
   kind. The five docker-mode CLI flags
   (`--docker-image`/`--docker-container-id`/`--dockerfile-path`/
   `--docker-image-file`/`--docker-keep`) and the config schema
   (`trae_agent/utils/config.py`) contain nothing else mount-related —
   confirmed by reading both, not just the one code path.
2. **Confirmed empirically, below.** A real `trae-cli run --docker-image
   ubuntu:latest --working-dir <scratch dir>` invocation, with decoys seeded
   in the real `$HOME`, produced zero reachability: the probe's own
   `mounted_volumes_detections` finding shows exactly one host mount (the
   working directory at `/workspace`), and no `/Users/...`,
   `.ssh`/`.aws`/`.gcloud`/etc. path appears anywhere in the report.
3. **Genuine vendor default, not a bug — and no extension point exists to
   add sharing without misrepresenting it.** Every real trae-agent user of
   `--docker-image` gets exactly this single-mount posture; there is no
   `DOCKER_FLAGS`-style passthrough in trae-agent itself (unlike gemini-cli's
   `--sandbox` flags). The stub script's own `DOCKER_FLAGS` shell array is
   purely internal plumbing for trae-cli's own arguments
   (`--docker-image <image>`), not a user-facing extension point, and it
   does not reach the container's mount table. The only way to get `$HOME`
   into the container would be `--docker-container-id` (attach to a
   container built outside trae's control) — that would be *our* harness
   constructing the environment again, the exact anti-pattern this map
   exists to fix for the original five runtimes, not an extension of trae's
   own default. **No fix recommended.**

## Environment

```
$ docker version --format '{{.Client.Version}} / server {{.Server.Version}} / {{.Server.Os}}/{{.Server.Arch}}'
29.6.1 / server 29.6.1 / linux/arm64
```

`trae-cli` is installed locally via `uv tool install --from
git+https://github.com/bytedance/trae-agent`, pinned to commit
`e839e559ac61bdd0e057c375dd1dee391fee797d` (per
`trae_agent-0.1.0.dist-info/direct_url.json` in the tool's venv) — this is
the actual vendor source read throughout, not a guess from docs.
`TRAE_SRC` (per the stub script's own comment: needed because docker mode's
`pyinstaller` tool-bundling step reads `trae_agent/tools/*.py` relative to
cwd) was **not** a pre-existing checkout on this machine — none was found.
It was supplied by copying the installed package's own `trae_agent/`
tree (which contains the same `tools/edit_tool_cli.py` /
`tools/json_edit_tool_cli.py` the build step needs) into a scratch
directory, since the installed site-packages tree *is* a valid source tree
for this purpose. `TRAE_CLI` was pointed at the venv's own `trae-cli` (not
the `~/.local/bin` shim) so the stub's `PATH`-prepending logic picks up the
venv's `pyinstaller` binary, which the vendor's `build_with_pyinstaller()`
shells out to unconditionally before any docker-mode run.

The probe binary had to be cross-compiled for the container's OS/arch
(`GOOS=linux GOARCH=arm64` — Docker Desktop's `desktop-linux` VM is
`linux/arm64` on this Mac); a macOS binary copied into `/workspace` fails
with `Exec format error`, unrelated to mount visibility.

Decoys were seeded on the real `$HOME` via the project's own
`scripts/seed-decoys.sh` (soft — writes only where nothing already exists):

```
$ bash scripts/seed-decoys.sh /tmp/probe-trae-research
seed-decoys: planted 0, skipped 26 (already present / unwritable)
```

("0 planted" because sibling research sessions on this machine had already
seeded the same idempotent set — confirmed present, e.g.
`/Users/cns/.aws/credentials` → `sandbox-probe decoy — not a real secret.
Safe to delete.`)

## Experiment — real `trae-cli --docker-image` run, decoys already in `$HOME`

Full command (env vars match the stub script's contract:
`PROBE`, `OUT`, `TRAE_DOCKER=on`, `TRAE_CLI`, `TRAE_SRC`):

```
$ PROBE=/tmp/probe-trae-research-linux \
  OUT=.../trae-out/report.json \
  TRAE_DOCKER=on \
  TRAE_CLI=/Users/cns/.local/share/uv/tools/trae-agent/bin/trae-cli \
  TRAE_SRC=.../scratchpad/trae-src \
  bash scripts/run-probe-via-trae-stub.sh
```

Relevant excerpts from the real run (stubbed model, real Docker, real
trae-agent):

```
Docker mode enabled. Using image: ubuntu:latest
...
Starting a new container from image: ubuntu:latest...
Container ccd3d23cc86d created. Workspace '/var/folders/.../tmp.KrZQ0uZRLG'
  is mounted to '/workspace'.
Copying tools from '.../trae_agent/dist' to container path '/agent_tools'...
Persistent shell is ready.
...
[mock-agent-api] openai bash -> /workspace/probe scan --tasksets baseline
  --tags runner=Darwin,harness=trae-docker,... --output_path /workspace/report.json
...
trae(stub, docker=on) wrote .../trae-out/report.json
```

The probe's own report, run *from inside* trae's real container:

```
$ jq '.findings[] | select(.findingType=="mounted_volumes_detections")' report.json
{
  "findingType": "mounted_volumes_detections",
  "task": "baseline_mount_scanner",
  "description": "Host-mounted volumes",
  "value": [
    "/run/host_mark/private -> /workspace (fakeowner)",
    "/dev/vda1 -> /etc/resolv.conf (ext4)",
    "/dev/vda1 -> /etc/hostname (ext4)",
    "/dev/vda1 -> /etc/hosts (ext4)"
  ]
}
```

Exactly one host mount — the working directory at `/workspace` (Docker
Desktop's virtiofs `host_mark` path is the VM-hop implementation detail seen
in the docker-default-nesting research; not trae-specific) — plus Docker's
standard per-container network-identity files. No second entry, no `$HOME`,
no credential directory. Searching the *entire* report for anything under
the real `$HOME` or any credential filename found nothing:

```
$ jq '[.findings[].value] | flatten | map(select(type=="string")) | .[] | select(test("cns|home|ssh|aws|gcloud|kube"; "i"))' report.json
(no output — nothing matched)
```

`sensitive_readable_paths` for this run is entirely container-native
(`/etc/passwd`, `/etc/shadow`, `/proc/1/environ`, `/root`, `.dockerenv`,
etc.) — none of it originates from the seeded parent host. Networking, by
contrast, *is* shared (bridge, not `--network=none`): the report's
`external_host_dns_resolution` and `external_host_connectivity` findings
show real DNS resolution and a successful connection to `google.com` —
consistent with `DockerManager.start()` never passing a `network=` kwarg to
`containers.run()`, so Docker's own bridge default applies unmodified, same
as the plain-`docker run` finding in `docker-default-nesting.md`.

## Practical consequence for the map

Unlike the original five `sandbox`-family runtimes, `TRAE_DOCKER=on` is
already **correctly nested**: it launches a real container, via the real
Docker daemon, on the real seeded host, using trae-agent's own unmodified
mount logic — not a disconnected environment the harness built by hand. The
narrow result (decoys unreachable) is not an artifact of how the stub
invokes it; it's what `DockerManager.start()` does for every user of
`--docker-image`, by construction, with no override. Extending sharing here
would require either patching vendor source (misrepresents trae's real
default for everyone else) or swapping to `--docker-container-id` against a
container the harness built itself (reintroduces the exact
harness-constructs-its-own-environment pattern the map exists to eliminate
for the original five). **No fix needed; this ticket resolves as a
legitimate vendor-default "blocked" result**, in the same vein as gVisor's
"no implicit default" finding but stricter — gVisor exposes one array entry
to opt in per-mount, trae-agent's docker mode exposes none.

## Cleanup

Both containers created during this research (`ccd3d23cc86d`,
`1959fc9165ea`) were stopped and removed after the runs
(`docker stop`/`docker rm`); `docker ps -a` shows none left running. The
scratch `TRAE_SRC` copy and pyinstaller build output live entirely under
this session's scratchpad and the trae-agent venv's own directory, not
under the seeded `$HOME`.
