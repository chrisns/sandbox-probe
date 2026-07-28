# Research: are firejail/nono/srt's restriction flags their own defaults?

Source ticket: `.scratch/sandbox-canary-nesting/issues/06-firejail-nono-srt-flag-audit.md`

## Question

`scripts/run-probe-in-sandbox.sh` restricts the *same* filesystem for
`srt`/`firejail`/`nono` (policy-based, no fresh rootfs) rather than swapping
in a disconnected environment like `docker`/`podman`/`bwrap`/`nspawn`/`gvisor`
do — so these three are out of scope for the core nesting bug. But each
invocation adds an explicit restriction flag:

- `firejail --quiet --net=none --seccomp "${CMD[@]}"`
- `nono run --silent --allow-cwd --allow "$(dirname "$OUT_ABS")" --block-net "${CMD[@]}"`
- `srt --settings "$SETTINGS" "${CMD[@]}"`, `$SETTINGS` = deny-by-default
  filesystem (write allowed only to `${PWD}`/`/tmp`/`/private/tmp`), empty
  network allow/deny lists

For each: does the flag reflect that tool's own default/out-of-the-box
behavior with zero flags, or is it a project-added narrowing beyond default
(the same category of problem as docker/podman's fresh rootfs, just without
the disconnection symptom)?

## Answer, up front

Mixed. **`srt`'s policy matches its own documented default exactly** —
network deny-all and filesystem write-deny-all are `srt`'s out-of-the-box
behavior with *no* settings file at all; the script's JSON just makes that
explicit (and widens write access to `$PWD`/tmp only as far as needed for
the probe to emit its report). **`firejail --net=none` and `nono
--block-net` are both project-added narrowings**: both tools ship with
networking *open* by default, and each flag is what turns it off. This is
the same shape of problem flagged for docker/podman, just scoped to
networking only (filesystem access on both tools is unaffected — see below —
so the "same filesystem" premise holds; only the network-openness premise
was wrong for firejail/nono).

## srt (`@anthropic-ai/sandbox-runtime`)

Installed locally: `srt --version` → `1.0.0` (npm package `0.0.67`, macOS
arm64, `sandbox-exec`/Seatbelt backend). No `~/.srt-settings.json` existed on
this machine, so "bare `srt`, zero flags" is genuinely the tool's shipped
default, not a locally-customized one.

**Doc source** — `README.md` from the published npm tarball
(`npm pack @anthropic-ai/sandbox-runtime`, package `0.0.67`):

> "designed with a **secure-by-default** philosophy... processes start with
> minimal access, and you explicitly poke only the holes you need."
>
> "**Write** (allow-only pattern): By default, write access is denied
> everywhere. You must explicitly allow paths... An empty allow list means
> no write access."
>
> "**Read** (deny-then-allow pattern): By default, read access is allowed
> everywhere."
>
> "**Network Isolation** (allow-only pattern): By default, all network
> access is denied... An empty allowedDomains list means no network access."

**Empirical confirmation** (bare `srt` command, no `--settings`, no
`~/.srt-settings.json` present):

```
$ srt -- curl -s -o /dev/null -w "%{http_code}\n" --max-time 5 https://example.com
000
exit=56                              # network: blocked, matches documented default

$ srt -- sh -c 'echo hi > /tmp/srt-test-$$.txt'
sh: /tmp/srt-test-...: Operation not permitted    # write: blocked, matches documented default

$ srt -- sh -c 'echo hi > $HOME/srt-test-home.txt'
sh: /Users/cns/srt-test-home.txt: Operation not permitted   # write: blocked, matches documented default

$ srt -- cat "$HOME/.zshrc"     # (first ~3 lines printed successfully)
$ srt -- cat /etc/hosts          # (printed successfully)
                                  # read: allowed, matches documented default
```

**Verdict**: the script's `SETTINGS` JSON (deny network, deny write except
`$PWD`/`/tmp`/`/private/tmp`, allow read) is not a narrowing beyond default —
network policy and default-deny-write match `srt`'s shipped behavior with
zero configuration. The only deviation is *widening* write access to
`$PWD`/tmp, which is the minimum needed for the probe to write its own
report; without it, `srt`'s true zero-config default (write denied
*everywhere*, including `$PWD`) would make the probe unable to run at all.
This is `srt` correctly nesting in the seeded parent under a real,
essentially-default policy.

## firejail

Not installable on macOS (Linux SUID/namespace/seccomp tool, per the
script's own header comment). Tested empirically in a disposable Ubuntu
22.04 container on Docker Desktop's `desktop-linux` VM (`arm64`,
`--cap-add=ALL --security-opt seccomp=unconfined` so firejail's own
namespace/seccomp primitives aren't pre-blocked by the outer container):

```
$ apt-get install -y firejail    # firejail version 0.9.66

$ firejail --quiet -- curl -s -o /dev/null -w "%{http_code}\n" --max-time 5 https://example.com
200                               # network: NOT blocked by default

$ echo secret > /root/secret.txt
$ firejail --quiet -- cat /root/secret.txt
secret                            # filesystem read: NOT blocked by default

$ firejail --quiet -- bash -c 'echo hi > /tmp/fj-test.txt && cat /tmp/fj-test.txt'
hi                                # filesystem write: NOT blocked by default
```

**Doc source** — firejail's own man page source
(`src/man/firejail.1.in`, fetched from `github.com/netblue30/firejail`
master branch — the primary/canonical documentation for the tool):

> `--net=none`: "Enable a new, unconnected network namespace... Use this
> option to deny network access to programs that don't really need network
> access." (an opt-in namespace request, not something firejail sets up
> unasked)
>
> `--seccomp`: "Enable seccomp filter and blacklist the syscalls in the
> default list" (the filter itself is only installed once `--seccomp` is
> passed; "default list" refers to *which syscalls* get blacklisted once
> enabled, not to seccomp being on by default)

Supporting: `firejail.wordpress.com`'s own "Basic Usage" documentation
states network/filesystem restrictions are opt-in — no networking feature is
configured in firejail's own shipped profiles apart from where a profile
explicitly requests `net none`.

**Verdict**: both `--net=none` and `--seccomp` are project-added
restrictions, not firejail's default. Bare `firejail <cmd>` with zero flags
leaves networking fully open and applies no seccomp syscall filtering — it
only gives the process its own PID/mount-namespace view (per the man page's
`DESCRIPTION`), not the sandbox_probe-relevant network/syscall confinement
the script is testing. This mirrors the docker/podman category of problem
(testing an artificial configuration), but narrower in scope: it affects only
the network-openness premise, not the "same filesystem" premise — firejail
still shares the real host filesystem in both configurations (confirmed
above: `/root/secret.txt` was readable even with the flags absent), so
firejail is still correctly *nested* in the seeded parent. It just isn't
being tested at a default policy level for network/seccomp; it's being
tested at a policy the project chose to add.

## nono

Installed locally: `nono --version` reachable via `/opt/homebrew/bin/nono`
(macOS arm64, Seatbelt backend for this platform per the script's header
comment).

**Doc source** — `nono run --help` (the tool's own primary/authoritative
CLI reference, no separate man page found):

```
--block-net
    Block outbound network access (allowed by default)
```

This is unambiguous and from the tool itself: network access is open by
default; `--block-net` is what closes it.

**Empirical confirmation**:

```
$ nono run --silent -- curl ...                     # no --allow-cwd
nono: CWD access requires --allow-cwd in non-interactive mode
                                   # filesystem: deny-by-default, must opt in explicitly (unlike network)

$ nono run --silent --allow-cwd -- curl -s -o /dev/null -w "%{http_code}\n" --max-time 5 https://example.com
200                                # network: NOT blocked without --block-net, confirms --help text

$ nono run --silent --allow-cwd --block-net -- curl -s -o /dev/null -w "%{http_code}\n" --max-time 5 https://example.com
000
exit=7                             # network: blocked once --block-net is added

$ nono run --silent --allow-cwd -- cat "$HOME/.zshrc"      # outside allowed cwd
cat: /Users/cns/.zshrc: Operation not permitted    # filesystem: --allow-cwd scopes access to cwd only, read-only

$ nono run --silent --allow-cwd -- sh -c 'echo hi > ./nono-test.txt'
/bin/sh: ./nono-test.txt: Operation not permitted  # --allow-cwd alone is read-only (per --help:
                                                    # "level set by profile, defaults to read-only")
```

**Verdict**: `--block-net` is a project-added restriction beyond nono's
default (nono's own `--help` text states network is "allowed by default").
Filesystem behavior, however, is the opposite of network: nono denies *all*
filesystem access with zero flags (a bare `nono run` with no `--allow-cwd`
refuses to start at all in non-interactive mode) — so the script's
`--allow-cwd` (read-only cwd) plus `--allow "$(dirname "$OUT_ABS")"`
(read+write report dir) aren't a narrowing at all, they're the *minimum*
grant needed to run anything, and they're what correctly keeps nono nested
in the real, seeded filesystem rather than swapping in a fresh one. Only the
network flag (`--block-net`) is testing a policy nono doesn't ship with by
default.

## Summary

| Tool | Flag audited | Matches tool's own default? |
|---|---|---|
| `srt` | deny-by-default JSON (fs write + network) | **Yes** — documented and empirically confirmed as `srt`'s zero-config default |
| `firejail` | `--net=none`, `--seccomp` | **No** — both opt-in; bare firejail has open networking and no seccomp filter |
| `nono` | `--block-net` | **No** — nono's own `--help` states network is "allowed by default" |
| `nono` | `--allow-cwd` / `--allow <dir>` (filesystem) | **Yes** — nono denies all filesystem access with zero flags; these are the minimum grant to run at all, not a narrowing |

All three remain correctly *nested* in the seeded parent filesystem
(unlike docker/podman/bwrap/nspawn/gvisor) — none of them swap in a fresh
rootfs. The only finding here is narrower: firejail's and nono's *network*
restriction flags test a policy each tool's vendor does not apply by
default, so today's firejail/nono results represent "the project's chosen
network policy layered onto the tool" rather than "the tool's own
out-of-the-box behavior" for the network dimension specifically.

## Sources

- `@anthropic-ai/sandbox-runtime` README, npm package `0.0.67`
  (`npm pack @anthropic-ai/sandbox-runtime`), also mirrored at
  https://github.com/anthropic-experimental/sandbox-runtime
- firejail man page source, `src/man/firejail.1.in`,
  https://github.com/netblue30/firejail (master branch)
- firejail "Basic Usage" documentation, https://firejail.wordpress.com/documentation-2/basic-usage/
- `nono run --help` (installed binary, `/opt/homebrew/bin/nono`)
- Empirical tests: `srt`/`nono` run natively on macOS (arm64, this machine);
  `firejail` 0.9.66 run inside a disposable `ubuntu:22.04` container on
  Docker Desktop's `desktop-linux` VM (arm64), with
  `--cap-add=ALL --security-opt seccomp=unconfined` so the outer container
  doesn't itself block firejail's namespace/seccomp primitives
