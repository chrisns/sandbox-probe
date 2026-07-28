# Research: does gemini-cli's own `--sandbox docker` share the seeded parent's `$HOME`?

Source ticket: `.scratch/sandbox-canary-nesting/issues/13-gemini-docker-nesting.md`
Map: `.scratch/sandbox-canary-nesting/map.md`

## Question

`scripts/run-probe-via-gemini-stub.sh` with `GEMINI_SANDBOX=docker` re-execs the
**entire gemini-cli** inside a container via gemini-cli's own `--sandbox` flag —
this is Google's own docker invocation (`@google/gemini-cli`), not something the
project's harness constructs, unlike the original five `sandbox`-family runtimes
in `run-probe-in-sandbox.sh`. The stub's own comment says this "mounts the
workspace." Three things needed answers:

1. What does gemini-cli's own `--sandbox docker` mode mount by default — the
   project directory only, or does it also touch `$HOME`?
2. Empirically: with decoys seeded in `$HOME`, are they reachable from inside
   gemini-cli's own container?
3. If not reachable, is that a *meaningful* vendor-shipped boundary (don't force
   sharing in artificially), or the same "never actually nested" bug as the
   original five runtimes?

## Answers, up front

1. **Narrower than `$HOME`, but broader than "just the workspace."** gemini-cli's
   docker sandbox mounts a small, fixed allowlist of host paths, all built the
   same way the workspace is (an exact-absolute-path bind mount, no rootfs
   swap): the workspace directory, `$HOME/.gemini` (mounted at two container
   paths for consistency), the host's temp directory, `~/.config/gcloud`
   (read-only, if present), and the file named by
   `$GOOGLE_APPLICATION_CREDENTIALS` (read-only, if set). **The rest of `$HOME`
   is not shared** — full-`$HOME` sharing only happens if `GEMINI_CLI_HOME` is
   set to something that differs from the real OS home directory, which is not
   the default case. The project's own `sensitive_readable_paths` decoy targets
   (`sandbox-probe list-targets`, `scope: home`, `seedable: true` — `~/.ssh/*`,
   `~/.aws/*`, `~/.npmrc`, `~/.kube/config`, etc.) do not fall under any of the
   allowlisted subpaths, so per source they should be genuinely unreachable —
   this is a real "blocked" result, not a meaningless one.
2. **Not confirmed live.** Every empirical attempt on this machine (macOS,
   Docker Desktop) resulted in gemini-cli silently choosing **macOS Seatbelt**
   instead of Docker, despite `GEMINI_SANDBOX=docker` (and separately,
   `"tools":{"sandbox":"docker"}` in settings.json) explicitly requesting
   docker — see "What blocked live verification" below. This is a macOS-only
   artifact of the dev machine used for this research; `scan-matrix.yaml` runs
   the `harness: gemini-docker` row on `ubuntu-latest`, where Seatbelt does not
   exist and this preference cannot fire, so it should not affect CI. It's
   flagged here as an independent, possibly reportable observation, not a
   blocker for this ticket's actual finding.
3. **Different in kind from the original five.** gemini-cli's docker sandbox
   genuinely nests the container in the seeded parent host: the workspace is
   bind-mounted at its literal host path (no fresh rootfs), and a curated,
   named set of other host paths get the same treatment. This is a real,
   vendor-shipped, out-of-the-box default boundary — not an accidental
   disconnection. Nothing in this ticket needs the "fix" the sibling runtime
   tickets do; there is no "never nested" bug to correct here.

## Where this comes from

`gemini` (npm `@google/gemini-cli`, version `0.49.0`, installed globally via
`npm install -g @google/gemini-cli`) ships as a minified esbuild bundle at
`$(npm root -g)/@google/gemini-cli/bundle/gemini-*.js`. The mount-construction
logic was read directly out of that installed bundle (function names and
control flow survive esbuild's bundling, just not variable names), then
independently verified against the **actual, non-minified TypeScript source**
on GitHub at the exact `v0.49.0` tag
(commit `5d402f89f2075d570eda73677dba47f789ea15bc`) — the two are logically
identical everywhere it mattered for this question, including one place where
the installed bundle has an esbuild-time-inlined literal
(`GEMINI_SANDBOX_IMAGE_DEFAULT` → the literal string
`us-docker.pkg.dev/gemini-code-dev/gemini-cli/sandbox:0.49.0`) where the
GitHub source reads it from `process.env` at runtime — a packaging detail, not
a behavior difference.

- [`packages/cli/src/utils/sandbox.ts`](https://github.com/google-gemini/gemini-cli/blob/5d402f89f2075d570eda73677dba47f789ea15bc/packages/cli/src/utils/sandbox.ts) — builds the `docker run` argument list (`start_sandbox`).
- [`packages/cli/src/config/sandboxConfig.ts`](https://github.com/google-gemini/gemini-cli/blob/5d402f89f2075d570eda73677dba47f789ea15bc/packages/cli/src/config/sandboxConfig.ts) — resolves which sandbox command to use (`getSandboxCommand`/`loadSandboxConfig`).
- [`packages/cli/src/gemini.tsx`](https://github.com/google-gemini/gemini-cli/blob/5d402f89f2075d570eda73677dba47f789ea15bc/packages/cli/src/gemini.tsx#L569-L593) — the bootstrap gate that decides whether to hop into a sandbox at all.
- The bundled/hosted doc (`docs/cli/sandbox.md`, shipped inside the npm package and mirrored at [google-gemini/gemini-cli `docs/cli/sandbox.md`](https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/sandbox.md)) is **not fully accurate** here — see "Doc vs. code" below.

## The default mount set (from source)

`sandbox.ts`, `start_sandbox()`, docker/podman branch — every `args.push('--volume', ...)` that runs unconditionally or under a condition that's true out of the box:

```ts
// sandbox.ts:314
const args = ['run', '-i', '--rm', '--init', '--workdir', containerWorkdir];
...
// mount current directory as working directory in sandbox (set via --workdir)
args.push('--volume', `${workdir}:${containerWorkdir}`);

// mount user settings directory inside container, after creating if missing
// note user/home changes inside sandbox and we mount at BOTH paths for consistency
const userHomeDirOnHost = homedir();
const userSettingsDirInSandbox = getContainerPath(`/home/node/${GEMINI_DIR}`);
...
const userSettingsDirOnHost = path.join(userHomeDirOnHost, GEMINI_DIR);   // $HOME/.gemini
...
args.push('--volume', `${userSettingsDirOnHost}:${userSettingsDirInSandbox}`);
if (userSettingsDirInSandbox !== getContainerPath(userSettingsDirOnHost)) {
  args.push('--volume', `${userSettingsDirOnHost}:${getContainerPath(userSettingsDirOnHost)}`);
}

// mount os.tmpdir() as os.tmpdir() inside container
args.push('--volume', `${os.tmpdir()}:${getContainerPath(os.tmpdir())}`);

// mount homedir() as homedir() inside container
if (userHomeDirOnHost !== os.homedir()) {           // only true if GEMINI_CLI_HOME overrides homedir()
  args.push('--volume', `${userHomeDirOnHost}:${getContainerPath(userHomeDirOnHost)}`);
}

// mount gcloud config directory if it exists
const gcloudConfigDir = path.join(homedir(), '.config', 'gcloud');
if (fs.existsSync(gcloudConfigDir)) {
  args.push('--volume', `${gcloudConfigDir}:${getContainerPath(gcloudConfigDir)}:ro`);
}

// mount ADC file if GOOGLE_APPLICATION_CREDENTIALS is set
if (process.env['GOOGLE_APPLICATION_CREDENTIALS']) { ... args.push('--volume', ...); }
```

`homedir()` (from `@google/gemini-cli-core`) is a thin wrapper:
`GEMINI_CLI_HOME` env override if set, else the real `os.homedir()`. So
`userHomeDirOnHost !== os.homedir()` is false — and the whole-`$HOME` mount
skipped — on every normal invocation, including the project's stub (it never
sets `GEMINI_CLI_HOME`).

So, out of the box, on a machine with no `~/.config/gcloud` and no
`GOOGLE_APPLICATION_CREDENTIALS`, gemini's docker sandbox shares exactly:
workspace, `$HOME/.gemini`, and the host temp dir. On this dev machine
`~/.config/gcloud` also exists, so that's a fourth, read-only share — still
nowhere near the project's seedable decoy paths (`~/.ssh/*`, `~/.aws/*`,
`~/.npmrc`, `~/.kube/config`, `~/.docker/config.json`, etc. — confirmed via
`sandbox-probe list-targets`, none of which live under `.gemini`, `.config/gcloud`,
or a temp dir).

Two further, genuinely opt-in extension points exist and were **not** used by
default:

- `SANDBOX_MOUNTS` (env var, read by the same host-side code, `from:to:opts`
  syntax) — the vendor's own documented way to add host paths outside the
  workspace. This is the mechanism a real gemini-cli user would use if they
  wanted more sharing — and it's a deliberate per-invocation opt-in, not part
  of the default.
- `config.allowedPaths` (`settings.json` → `tools.sandbox.allowedPaths`) — same
  effect, settings-file-driven instead of env-driven.

## Doc vs. code

The bundled `docs/cli/sandbox.md` says, under "Including files outside the
workspace": *"By default, the sandbox only has access to the current project
workspace."* That's true in spirit (no blanket `$HOME` access) but **undersells
what's actually shared** — it omits `$HOME/.gemini`, the temp directory, and
the conditional `~/.config/gcloud` / ADC-file mounts entirely. Worth flagging
upstream at some point; not blocking for this ticket, since none of those
extra shares intersect the probe's decoy targets anyway.

## `SANDBOX_FLAGS` reasoning (per the ticket's guardrail)

The stub's `SANDBOX_FLAGS` currently injects
`--add-host host.docker.internal:host-gateway --entrypoint "" -e GEMINI_CLI_TRUST_WORKSPACE=true -e GEMINI_API_KEY=dummy -e GOOGLE_GEMINI_BASE_URL=...`
— purely mock-plumbing (routing + auth for the stubbed model), zero mount
flags. `SANDBOX_FLAGS` is parsed as literal extra `docker run` arguments
(`sandbox.ts` line ~325, immediately after building the base `args` array,
before the volume-mount block above), so it technically *could* carry an
arbitrary `--volume $HOME:$HOME` — but doing so would not be "gemini's vendor
default" any more, it would be the harness constructing its own sharing, the
exact thing this whole map exists to avoid. **Recommendation: don't.** The
project's seedable decoys already don't intersect gemini's default share set,
so there's nothing to unblock — extending the mount would test a boundary no
default gemini-cli docker-sandbox user actually has. If broader sharing is
ever genuinely needed for this harness, the vendor-supported knob is
`SANDBOX_MOUNTS` (set on the *outer*, pre-hop `gemini` invocation, same as
`GEMINI_SANDBOX` — it's read by the host-side arg-builder, not something
`SANDBOX_FLAGS` can reach into), and even that would need to be justified as
matching real-user opt-in behavior, not just "make the comparison work."

## What blocked live verification

Built the probe and seeded `$HOME` via the project's own scripts:

```
$ make build
$ ./bin/sandbox-probe list-targets | jq -r '.[] | select(.seedable) | .path'
/Users/cns/.ssh/id_rsa
/Users/cns/.ssh/id_ed25519
... (25 total, all under $HOME, none under .gemini/.config/gcloud/tmp)
$ ./scripts/seed-decoys.sh ./bin/sandbox-probe
seed-decoys: planted 0, skipped 26 (already present / unwritable)
```

(All 26 targets already existed from a prior session's soft-seeding, or in
one case — `~/.ssh/id_rsa` — a real pre-existing key; `seed-decoys.sh`'s
soft/never-clobber guarantee held, nothing was touched or read.)

Then ran the stub with the docker backend:

```
$ PROBE="$(pwd)/bin/sandbox-probe" OUT="$(pwd)/.research-scratch/gemini-docker-report.json" \
  GEMINI_SANDBOX=docker RUNNER=manual-research bash scripts/run-probe-via-gemini-stub.sh
```

Every run (four total, varying stdin handling, `DEBUG=1`, and whether
`docker` was requested via `GEMINI_SANDBOX` or via `settings.json`
`tools.sandbox`) produced the same shape of failure: repeated
`fetch failed sending request` against `host.docker.internal` (a hostname only
meaningful *inside* a container) followed by the stub reporting no output
file. Concurrently monitoring `docker events --since 0s` across every run
showed **zero container-lifecycle events** — the daemon never saw a `create`,
so nothing was ever spawned. `host.docker.internal` also doesn't resolve from
the host shell itself (`ping host.docker.internal` → `Unknown host`), which is
consistent with the process running directly on the host instead of inside a
container.

Root-caused as far as reasonably possible without live-patching the installed
package (blocked by this environment's own tool-permission classifier, which
is correct to refuse editing files outside the repo): a minimal, non-mocked
repro (`GEMINI_SANDBOX=docker gemini --sandbox --approval-mode=yolo -p "hello"`,
and separately with `docker` set via `settings.json` `tools.sandbox` instead of
the env var) both printed gemini-cli's own debug line
`using macos seatbelt (profile: permissive-open) ...` — i.e. **gemini-cli chose
macOS Seatbelt over an explicitly-requested Docker sandbox**, on this machine,
every time, regardless of which supported mechanism (`GEMINI_SANDBOX=docker`
env var or `settings.json`) was used to request docker. This reproduced
consistently (4/4) but its exact root cause inside `getSandboxCommand()`
wasn't pinned down within this ticket's scope.

**This is very likely irrelevant to the actual pipeline.** `scan-matrix.yaml`
runs the docker row on Linux, not macOS:

```yaml
# .github/workflows/scan-matrix.yaml
- { os: linux, runner: ubuntu-latest, ..., family: gemini, harness: gemini-docker, backend: docker, expect: '["docker"]' }
- { os: macos, runner: macos-latest,  ..., family: gemini, harness: gemini-sandbox-exec, backend: sandbox-exec, expect: '["seatbelt"]' }
```

`sandbox-exec` doesn't exist on Linux, so `getSandboxCommand()`'s
darwin-only auto-detect branch (`os.platform() === 'darwin' && commandExists.sync('sandbox-exec')`)
can't fire there regardless of whatever caused it to fire on this Mac. This
looks like a macOS-only quirk surfaced by testing gemini's Linux-targeted
`docker` backend on the wrong platform, not a defect in the project's
harness or in `scan-matrix.yaml`'s existing `expect: '["docker"]'` assertion.
It's noted here in case it's useful to a maintainer, but is out of scope to
chase further for this ticket.

## Practical consequence

No fix needed for this ticket, unlike the sibling runtime tickets (01–06).
gemini-cli's own docker sandbox already launches as a genuine child of the
seeded parent host — the workspace and a small, named set of other host paths
(`$HOME/.gemini`, temp dir, optionally `~/.config/gcloud` and an ADC file) are
real bind mounts at their real host paths, not a fresh rootfs. The project's
seedable decoy targets sit outside that allowlist, so — once a live run can
actually be completed (on Linux, matching CI, where the Seatbelt-preference
quirk above doesn't apply) — an "unreachable" result for those decoys would be
a genuine, meaningful "blocked" finding, not evidence of a disconnected
test harness. `SANDBOX_FLAGS` should stay mock-plumbing-only; it should not be
extended to add a `$HOME` mount, because that would test a boundary no normal
gemini-cli docker-sandbox user has by default.
