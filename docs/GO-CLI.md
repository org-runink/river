# Go, not shell: the `river` CLI

Runink River's internals are moving from shell scripts to Go (decided 2026-09-28). This page
is the contract for that move: what the code looks like, what may stay shell, and the order the
scripts are ported in. `river lint shell-ratchet` enforces it in Tier 1.

## Why

About 15,000 lines of shell build, install and run the image. Shell has no types and no unit
tests, and it fails quietly: an unset variable or an unchecked pipeline stage turns into a
broken image hours later. Go gives the same programs types, tests, `go vet`, real error values
and one static binary, and it is already the language of the installer, the planner and the
guide.

## The shape of the code

### `pkg/pipe`: the pipeline pattern, standard library only

`github.com/org-runink/river/pkg` is its own module with **no third-party dependency**, so
anything, including a downstream image's own tooling, can import it. `pipe` covers what the
scripts did with pipes and loops.

- **Command pipelines** replace `a | b | c`. `pipe.Run` starts every stage at once and joins
  each stdout to the next stdin with an OS pipe. It fails like `set -o pipefail`: any stage
  that exits non-zero fails the run, and the error names the stage and quotes the end of its
  stderr.

  ```go
  // git ls-files | grep -c '\.sh$'   (but typed, and pipefail)
  out, err := pipe.Output(ctx, nil,
      pipe.Cmd("git", "-C", repo, "ls-files"),
      pipe.Cmd("grep", "-c", `\.sh$`))
  ```

  Arguments never pass through a shell, so there is no quoting to get wrong. `Command.Env`
  is the `FOO=1 cmd` of a script, and `Command.Dir` is its `cd dir && cmd`.

- **Data pipelines** replace `while read -r line` and `xargs -P`. A `pipe.Pipeline` owns a
  context. `Source`, `Slice` and `Lines` produce values into channels; `Map`, `Filter` and
  `ParallelMap` are stages, each in its own goroutine; `Collect` and `Drain` end it. The
  first error cancels every stage, as `set -e` stopped a script, and no goroutine outlives
  the call.

  ```go
  p := pipe.New(ctx)
  paths := pipe.Lines(p, manifest)
  sums := pipe.ParallelMap(p, paths, runtime.NumCPU(), sha256File)
  got, err := pipe.Collect(p, sums)
  ```

Prefer the standard library to a child process whenever one exists: `os`, `io/fs` and
`path/filepath` for files (never `cp`, `mv`, `install`, `find`), `crypto/sha256` rather than
`sha256sum`, `archive/tar` and `compress/*` for archives, `encoding/json` rather than `jq`.
Run a program only when it *is* the tool: `git`, `pacman`, `makepkg`, `zfs`, `mkfs.*`,
`qemu-*`, `s6-*`.

### `cli/`: one `river` command line (cobra + viper)

Every entry point is a subcommand of one binary, `river`, built from `cli/cmd/river`:

| Group | Replaces | Examples |
|---|---|---|
| `river lint …` | `scripts/lint-*.sh` | `river lint one-engine`, `river lint shell-ratchet`, `river lint reuse`, `river lint public-leak` |
| `river build …` | `build/*.sh` | the image stages, the kernel and ZFS build, the root stage |
| `river install …` | `installer/lib/*.sh` | the install steps the graphical installer drives |
| `river firstboot …` | the profile's first-boot scripts | one subcommand per step |
| `river service run <name>` | the logic in s6 `run` files | what a service does before it `exec`s its daemon |
| `river test …` | `tests/*.sh`, the QEMU harnesses | `river test memtune`, `river test installed-hooks` (later `river test qemu-gui`) |

Conventions for every command:

- **Flags, environment and config are one thing.** viper binds each flag to `RIVER_<FLAG>`
  (dashes become underscores) and to the config file (`--config`, else
  `/etc/river/river.yaml`, else `$XDG_CONFIG_HOME/river/river.yaml`). Precedence: command
  line, environment, config file, default. **A port keeps the environment variables its
  script read**, so `RIVER_PROFILE_DIR=… river build iso` works like
  `RIVER_PROFILE_DIR=… build/local-iso.sh` did.
- One package per group under `cli/internal/<group>`. A group registers itself with
  `rootcmd.Register` from `init`, and `cmd/river/main.go` imports it.
- The command's logic is an exported function taking a `context.Context` and writers, and the
  cobra `RunE` is a thin adapter over it, so tests call the function directly.
- Messages keep the old script's prefix (`lint-one-engine: …`), so logs, docs and people's
  habits still match.
- cobra and viper are **vendored** (`cli/vendor`), because Tier 1 builds with no network and
  the image is built from pinned inputs. `go mod vendor` after any dependency change.

## What may stay shell

A tool that only reads shell keeps its file, but only as a one-line call into `river`:

| File | Why it stays | What it may contain |
|---|---|---|
| `PKGBUILD`, pacman `.install` | makepkg and pacman source bash | the package metadata; `build()`/`package()` call `river` |
| s6 `run`, `finish`, `up`, `down` | s6 executes the file | at most two lines of code, one calling `river` (`exec river service run <name>`) |
| CI workflow `run:` lines | YAML runs a shell | a single `river …` call; no logic in YAML |
| the root stage | one `sudo` line a person types | `sudo river build root-stage <env-file>` |

Everything else is Go. `scripts/shell-ratchet.txt` lists the shell that exists today.
`river lint shell-ratchet` fails on a shell script that is not listed (new logic goes in Go),
and on a listed line whose script is gone (a port deletes its line in the same change). The
list only shrinks. PKGBUILDs, `.install` files and thin s6 files are exempt and never listed.

## Porting a script

1. Write the subcommand and its tests. Test the behaviour the script had, not the script:
   a fixture tree, the failure messages, the exit status. The first port,
   `lint-one-engine.sh` → `river lint one-engine`, is the worked example
   (`cli/internal/lint/oneengine.go`).
2. Keep the interface: the same environment variables, the same output the tests or
   downstream consumers read, and the same exit codes.
3. Repoint every caller (the Makefile, `scripts/ci-tier1.sh`, workflows, docs,
   `AGENTS.md`), delete the script, and delete its line from `scripts/shell-ratchet.txt`, all
   in one change.
4. A script that other trees run by path, such as the installer steps a downstream profile
   mirrors, keeps a one-line wrapper (`exec river install <step> "$@"`) for one release, and
   the wrapper stays listed until it goes.

A test whose subject is still shell (a sourced library, a hook runner) keeps driving that shell
as a program until its own port. `river test` does it with one constant script,
`sh -c '. "$1" && shift && "$@"' sh LIB FUNCTION ARGS...`, every value a positional
parameter, so nothing is ever spliced into shell text (`cli/internal/testcmd`).

`river test <name> --evidence FILE` also writes the run as a release-evidence document
(`runink.release-evidence/1`, `cli/internal/evidence`): harness `river-tier1/<name>`
(`river-dev/sddm-theme` for the developer check), the checkout's commit as the subject, and
one entry per check under a stable machine name. Each subcommand declares its names in a
static list; a declared check that never reports, or a reported one that was not declared,
fails the document, and a check that does not apply on a host is a skip with its reason. The
terminal output is unchanged, with or without the flag. Evidence is refused before the test
runs from a checkout with uncommitted changes or a binary built from a modified tree; a
document must name a clean commit for both the subject and the harness (the producer's own
checks cover the rest of the shape, the architecture included).

## Order

Lowest risk first. Each phase ends with Tier 1 green and, from phase 3 on, a full
`qemu-gui-test.sh` install of the image built from it.

1. **Repository tooling**: `scripts/lint-*.sh`, `scripts/ci-tier1.sh`, `tests/*.sh` (unit
   checks), `branding/render.sh`, `validation/run.sh`, `bench/`. Nothing here ships in the
   image.
2. **Installer steps**: `installer/lib/*.sh`, `installer/netsetup`, `installer/remediation`,
   and their mirror in the profile. The graphical installer calls `river install <step>`.
3. **The image's own programs**: the profile's `root-overlay/usr/local/bin` tools, first boot,
   and the logic in s6 service files (each `run` becomes `exec river service run <name>`).
   The `river` binary ships in the image from here on.
4. **The image build**: `build/local-iso.sh`, `build-kernel-zfs.sh`, `iso-root-stage.sh`,
   `k0s-airgap.sh`, the QEMU harnesses and their profile-hook contract. The one sudo line
   becomes `sudo river build root-stage …`.
5. **The public installer**: `install.sh`, the signature-verifying one-line installer.
   People run it with `curl … | sh`, so it may stay the last and smallest shell script, or
   become a static `river` download plus signature check; that decision is open.

Downstream images built on River follow the same pattern with their own CLI, importing
`pkg/pipe` and River's packages rather than copying them.
