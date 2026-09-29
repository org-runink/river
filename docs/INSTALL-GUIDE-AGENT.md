<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# river-guide: the install guide agent

> **Where it runs.** river-guide is built and packaged here (`guide/`, `build/pkgbuilds/river-guide`,
> the pinned model in `guide/model.lock`), but the Runink River medium does not carry it: that
> medium opens the graphical installer in its live desktop, and the 1.1 GB model would ride
> along unused. A downstream server distribution ships it on its own medium (docs/BUILD.md,
> "Downstream distributions"); what follows describes it there.

When the operator picks the **Runink River Sovereignty Server** entry at the install
medium's boot menu, the live system starts `river-guide` on the third console (tty3; tty1 shows the graphical installer). It
walks the operator through the Runink River install guide, answers questions about it with
citations, and runs the read-only checks the guide names. Optionally, it mirrors the install
to one GitHub issue, so an operator at another site can follow along and ask questions by
commenting `@river_install …`.

It runs fully offline. The guide itself touches the network only when the operator signs in
(`login`) or turns on the issue channel (`remote on`). The network-first step it proposes
(`river-netsetup --check`) reports what the live session already set up; see
[INSTALL.md](INSTALL.md#network-first).

Code: `guide/` is a Go module that uses only the standard library, MIT-licensed. The binary
is `river-guide`. Tests: `make test-guide`, and the `river-guide` CI job.

## How it boots

| Piece | Where | What it does |
|---|---|---|
| GRUB entries | `scripts/patch-artools.go` | `… Install / Live` (the default) starts the guide offline. `… Install / Live, remote guide (@river_install)` also passes `river.guide.remote=1`, so the guide offers the issue channel at start. |
| Model server | s6 longrun `river-guide-model` (`live-overlay/etc/s6/sv/`), which runs `/usr/local/bin/river-guide-model` | Serves the guide model with mistral.rs on `[::1]:8187` as `nobody`, offline. If it cannot, it writes `degraded: <reason>` to `/run/river-guide-model/state` and tells s6 not to restart it. |
| tty3 | `live-overlay/etc/s6/config/tty3.conf` sets `GETTY` to `/usr/local/bin/river-guide-tty` | s6's own `tty3` longrun runs the guide as the unprivileged live user, on tty3 as its controlling terminal. When the operator types `quit`, it hands tty3 to `agetty`. (Until the graphical installer, this was tty1; `river-installer-tty` still falls back to the guide on tty1 when the kiosk cannot run.) |
| Package | `build/pkgbuilds/river-guide` | Installs `river-guide`, the mistral.rs CPU server (sha256-pinned release) and a read-only copy of the guide markdown. |
| Model weights | `build/25-river-guide.sh` stages them into the live overlay | Fetched by the lock's revision and sha256. A LEAN build (`RUNINK_LEAN=1`) leaves them out, and the guide then runs degraded. |

Kernel command line switches, read by the scripts and by `river-guide`:

| Switch | Effect |
|---|---|
| `river.guide=0` | tty1 goes straight to the login prompt, and no model server starts. |
| `river.guide.model=0` | Degraded mode even on a machine that could run the model. |
| `river.guide.remote=1` | Offer the issue channel at start. The console operator still has to confirm it and sign in. |
| `river.guide.issue=OWNER/REPO/N` | The install-tracking issue. `OWNER/REPO` alone makes the guide open a new issue. |
| `river.guide.client_id=ID` | The GitHub App client ID for the device flow. It is not a secret. |
| `river.guide.proxy=URL` | HTTPS proxy for GitHub on an IPv6-only network. |
| `river.guide.bundle_key=ed25519:…` | The trust anchor for a platform guide bundle, supplied at fetch time. |

Nothing on the command line can switch remote approvals on. Only the console can do that
(`remote approvals on`), and a test enforces it.

**On an installed node none of this exists.** `20-clone-rootfs` excludes every river-guide
path from the clone, removes the package record, and gives tty1 back to `agetty`. The s6
service stays compiled in the database, finds no `river-guide-model`, and marks itself down.

## Answering questions: grounded, or not at all

1. **Retrieval.** A BM25 keyword index over the loaded guide sections picks the top four. It
   needs no embedding model, so it works the same with or without the guide model.
2. **Model mode.** The model sees only those excerpts, with rules to answer from them, cite
   `[bundle#anchor]`, and reply `NOT IN GUIDE` when they do not cover the question.
3. **Validation.** `river-guide` enforces the rules itself instead of trusting the model: -
   Every command in the reply (a backticked span, or a `$ `, `# ` or `sudo ` line) must
   appear in a retrieved excerpt's code. One invented command, and the whole model answer is
   withheld and replaced by the excerpts. The tests use `dd`, `wipefs` and `zpool destroy`
   as the invented commands. - At least 70% of the reply's content words must occur in the
   excerpts or the question. Below that, the reply is outside knowledge and is withheld. On
   the eval set, a reply that answered an out-of-scope question from the model's own
   knowledge was caught this way. - Citations to sections that were not retrieved are
   dropped. An answer with no citation is attributed to the excerpt it overlaps most.
4. **Degraded mode.** With no model, the answer is the matching sections, quoted, with their
   anchors.

Model output is text for a human. No code path executes it.

## Install steps

Steps are sections of a guide bundle that carry a directive:

```markdown
## Check the firmware mode
<!-- river-guide:step id=firmware kind=check run="ls /sys/firmware/efi" -->
```

| Kind | What river-guide may do |
|---|---|
| `info` | Show it. The operator marks it `done`. |
| `check`, `action` | Run `run=` after approval: argv, no shell. A pipe, `;`, `&`, `$`, backtick, redirection or brace in `run=` is refused when the bundle loads. |
| `destructive` | **Never run it.** Show the command. The operator runs it on another console (Alt+F2) and marks it `done`. |

The state machine (`guide/agent/steps.go`) steps pending → proposed → running →
done/failed/skipped. Only the console can propose (`next`), mark done, skip or retry. The
only command river-guide ever executes is the `run=` string of the bundle step that the
console proposed. The built-in Runink River steps are `network` (first: `river-netsetup
--check`, a read-only report of the network the live session set up before the guide
started; docs/INSTALL.md, "Network first"), `live-session`, `firmware`, `hwprobe`, `plan`,
`ipv6-address`, `recovery-key`, `install` (destructive: `sudo runink-install`), `reboot`,
`first-boot` and `verify`. A platform bundle's steps follow them.

The guide also answers questions about LAN installs (docs/INSTALL.md, "LAN installs"): its
sections "Let another machine install this one" and "Install other machines on this network"
explain both roles. They are sections, not steps: a pairing session opens the machine to the
LAN, so river-guide never starts one; the operator runs `sudo runink-install` on the second
console and picks the role there.

Hardware awareness comes from the installer's own read-only tools, called per their contract
(`docs/INSTALLER-HARDWARE.md`): `river-hwprobe --json` and `river-plan --probe F --manifest
/usr/local/share/runink/models.tiers --json`. When the medium has no `models.tiers`, the
guide passes `--no-models`, and it treats exit 3 (verdict `refused`) as a plan to show. The
summaries decode only non-identifying fields. They never read `serial`, `wwn`, `by_id`,
`confirm_id`, `model` or `mac`.

## The issue channel (`@river_install`)

- **Auth: the OAuth device flow, with no credential on the medium.** The console shows a
  code, and the operator enters it at github.com/login/device from any device. The token
  acts as that person, limited to what they and the GitHub App may do. It lives in process
  memory only. It is never written to disk, never logged, and is scrubbed from every
  outgoing comment.
- **Transport: outbound HTTPS polling (default 20 s), with no listening port.** GitHub has
  no IPv6 address, so an IPv6-only site needs NAT64/DNS64 or `river.guide.proxy=`. The error
  says so instead of a bare "no route to host".
- **Scope: one issue per install, labelled `river-install`.** The operator picks the
  repository. A private repository per client or site is recommended, and the guide warns
  and asks again when the repository is public.
- **Who counts.** Tags are honoured only from `OWNER`, `MEMBER` or `COLLABORATOR` accounts,
  or from the signed-in user. Others are ignored and audited.
- **What a tag can do:** `status`, `hw` and `plan` (all redacted), `approve [step]`, and any
  question. Questions are answered from **public bundles only**. `approve` works only for a
  non-destructive step that the console already proposed, and only while the console has
  `remote approvals on`. Command output never leaves the console.
- **What goes out:** generic progress, a redacted hardware summary and plan, and cited
  answers from the public guide. A platform step is named only as "a platform step". Every
  comment passes `guide/redact` last, which removes hostnames, IPv4 and IPv6 addresses,
  MACs, serials, WWNs, UUIDs, by-id paths, tokens, SSH and PEM keys, and long hex or base64
  runs. It then passes a private-text guard: any 7-word run from a private bundle withholds
  the whole comment.

## Guide bundles

The **built-in bundle** is the public Runink River install guide in
`guide/bundles/river/*.md`. It is compiled into the binary and installed read-only at
`/usr/share/river-guide/bundles/river/`.

A **platform guide bundle** is how a downstream platform adds its own installation guide
without putting it on the public medium:

- **Format.** `river-guide-bundle/v1`: one JSON file
  `{"format","name","version","docs":[{"path","content"}]}`, with markdown in the same
  dialect as above. Next to it is `<file>.sig`, a single line `ed25519:<base64 signature>`
  over the exact bytes.
- **Source.** Runink River ships **no** source descriptor and **no** key. A downstream
  medium adds `/etc/river-guide/sources.d/<name>.json` (`name`, `repo`, `path`, `ref`,
  optionally `client_id` and `pubkey`).
- **Trust anchor.** This is the ed25519 public key the bundle must verify against. It comes
  from the descriptor, or at fetch time from `river.guide.bundle_key=`, `--bundle-key`, or
  the console prompt during `login`. Runink River embeds no private key and no key
  fingerprint. A missing anchor means no bundle, never trust on first use.
- **Fetch.** After device-flow sign-in, the guide fetches the file with that identity's
  token. An identity that cannot read the repository gets a 404, and the guide says "no
  platform guide bundle loaded" and continues with Runink River steps. The signature, and
  the sha256 if one is pinned, are verified before parsing. A bundle that fails verification
  is refused loudly.
- **Where it lives.** In memory only. The fetched bytes are zeroed after parsing, and the
  parsed text is never written to disk, never logged (the audit log records section
  references only) and never posted.

## Choosing the guide model

The requirement was the smallest model that reliably answers from retrieved guide text,
under an Apache-2.0 or MIT licence, served by the same engine as the platform (mistral.rs,
CPU). Candidates were measured on 2026-09-24 with `river-guide eval` against the 15 fixed
questions in `guide/eval/cases.json`: 12 answerable, each with the sections a correct answer
may cite, and 3 out of scope, which must not get a model answer. The server was mistral.rs
v0.9.4's CPU release on `[::1]`, on a 16-thread laptop CPU that was also serving another
model.

| Candidate (GGUF) | Licence | Size | Result |
|---|---|---|---|
| **Qwen/Qwen2.5-1.5B-Instruct Q4_K_M** @ `91cad511` | Apache-2.0 | 1.07 GiB | **15/15**. Mean 4.6 s per answer at 8 threads and 6.5 s at 4 (13.6 s at 4 threads on the final run, with the host at load 20). RSS 1.6 to 2.5 GiB. **Chosen.** |
| ibm-granite/granite-3.3-2b-instruct Q4_K_M @ `7cdf86cc` | Apache-2.0 | 1.44 GiB | Does not load standalone in mistral.rs: its GGUF tokenizer (`refact` pre-tokenizer) needs a separate `tokenizer.json`, which is another download and another pin. Rejected. |
| Qwen2.5-3B-Instruct | Qwen Research licence | 1.96 GiB | Not Apache or MIT. Not evaluated. |

The first full run scored 14/15. Qwen answered "What CPU does Runink River need?" with
"x86-64-v3", correctly, but cited the hardware-probe section, which also states the
requirement. The case now accepts either section. Earlier runs are what motivated the
support check and overlap attribution in "Answering questions" above. Before them, the model
cited poorly and answered one out-of-scope question from its own knowledge; after them, both
were handled. The final per-question results are in `guide/eval/results/`.

The pin is `guide/model.lock`: HF repo, revision, file, size and sha256,
in the installer's `models.lock` column order, plus the mistral.rs release URL and sha256.
The guide model needs 7 GiB of RAM installed, 3 GiB free and AVX2. Below that, or on a LEAN
medium, the guide runs degraded, and every step works the same.

## Threat model

**Assets:** the target disk's data; the ZFS recovery key and the enrollment secrets typed
during the install; the operator's GitHub identity; the content of a private platform
bundle; identifying facts about the site (hostnames, addresses, serials, MACs).

**Trust boundaries:** the console, meaning whoever has physical or KVM access, is trusted.
The issue commenters are partly trusted. GitHub, the network, the model's output and the
public medium are not trusted.

| Threat | Mitigation |
|---|---|
| A remote commenter wipes a disk or runs arbitrary commands | Tags cannot propose steps. Destructive steps are never executed by river-guide, from any channel. Remote `approve` works only for a non-destructive step the console proposed, while the console has approvals on. Only collaborators are honoured. The commands come from the bundle, run as argv with no shell. |
| The model invents or alters a command (hallucination or prompt injection through guide text) | Model output is never executed. Commands in answers must match guide code verbatim, or the answer is withheld. Unsupported answers are withheld. |
| A secret or identifier leaks into a public issue | Summaries never decode identifying fields. Every comment is redacted last, and the token is in the redactor. The guide warns about public repositories. Command output is never posted. |
| A private platform bundle leaks | It is fetched only with an identity that can read it, and held only in memory. It is excluded from remote-scope retrieval, and a 7-word private-text guard runs on every comment. The audit log holds section references only. It is never on the medium. |
| A counterfeit or tampered platform bundle | It must verify against an ed25519 trust anchor supplied by the downstream or the operator (an optional sha256 pin is also checked), and step commands are syntax-restricted at load time. |
| A credential is left on the public medium | There is none to leave. The device flow needs only a non-secret client ID. The token is memory-only and dropped on exit. |
| A compromised or rogue model server | It is loopback-only, runs as `nobody`, and gets offline flags. The guide refuses a non-loopback model URL. Its output is untrusted text (see above). |
| Mass polling or rate limits | One issue, polled at 20 s with exponential backoff to 5 minutes. Comments are deduplicated by ID. |
| Guide artefacts reach an installed node | The clone excludes them, the package record is removed, tty1 is reset, and the s6 service marks itself down. |
| The live operator is not who they claim | Out of scope: physical console access already means control of the machine. The installer still requires typed serials and `YES` for destructive actions. |

**Residual risks:** the redactor is pattern-based and can miss a novel identifier format.
The private-text guard catches verbatim runs, not paraphrase; this is acceptable because no
private text is ever given to the remote-scope prompt. Commenters with write access to the
chosen repository are trusted to observe and ask. On a public repository, anyone can read
the progress, which is why the guide warns.

## Samples

A console session, from `TestConsoleSession`. It uses a fake model server and a fake GitHub.
The `ls` output is from the machine that ran the test.

```text
Runink River install guide dev (guide 2026.09.25) — install c31ca570
guide model: ready (a local model phrases answers from the guide; nothing leaves this machine)
no platform guide bundle loaded (1 source(s) configured: type `login` to fetch)
type `help` for commands, `next` to begin, or ask a question.
river> next

Step network: Set up the network first  [check]
Networking is the first step of every Runink River live session. …

→ `approve` runs: river-netsetup --check
river> approve
RESULT: lan-only
step network: done (exit 0)
river> next

Step live-session: Confirm the live session  [info]
…
river> done live-session
step live-session: done
Runink River steps 2/11.
river> next

Step firmware: Check the firmware mode  [check]
The directory `/sys/firmware/efi` exists only when the machine booted in UEFI mode. …

→ `approve` runs: ls /sys/firmware/efi
river> approve
config_table
efivars
…
step firmware: done (exit 0)
river> what happens to the recovery key?

The installer prints a 64-hex recovery key **once** and never writes it to any file, so record it offline before you continue [river#prepare-to-record-the-recovery-key].

river> should I wipe the disk first?

Run the installer [river#run-the-installer]
This step **erases (wipes) the target disk**: it partitions it and creates the pool. …
(model answer withheld: it contained a command that is not in the guide ("wipefs -a /dev/nvme0n1"))

river> login

On any device, open https://github.com/login/device and enter the code:

    ABCD-1234

signed in to GitHub as operator (token held in memory only)
Verification key for platform guide bundle "platform" (ed25519:…, blank to skip): ed25519:WOHY…
platform guide bundle platform 2026.09 loaded: 1 steps follow the Runink River steps (held in memory only)
river> remote on example-org/site-a
remote channel on: example-org/site-a#1 (comments `@river_install …`). Remote approvals are off (`remote approvals on` to allow).
river>
remote: @alice (remote) asks: does river support legacy BIOS boot?
```

An issue exchange, from `TestIssueCommentExchange` (abridged). A console-side test fixture
put a serial number and a hostname into the hardware summary on purpose, to prove that they
are redacted.

```text
── @operator
**Runink River install a1b2c3d4 started.** Guide mode: model. Remote approvals: off.
Hardware (redacted):
- Disk nvme0n1: 1863 GiB nvme (eligible)
- [redacted] on [redacted]
Progress: Runink River steps 0/10 done; platform steps 0/1 done.
── @alice   @river_install approve firmware
── @mallory @river_install approve firmware          (not a collaborator: ignored, audited)
── @operator
> @river_install approve firmware
Refused: remote approvals are off. The console operator can enable them with `remote approvals on`.
── @alice   @river_install zanzibar quokka bootstrap   (words only a private bundle contains)
── @operator
The loaded guide does not cover that. …
_Answers here come from the public Runink River guide only. Platform-specific guidance is shown on the install console._
── @alice   @river_install approve                      (after the console ran `remote approvals on`)
── @operator
Runink River step `firmware` (Check the firmware mode): **done** (exit 0). Output is shown on the console only.
── @alice   @river_install approve install
── @operator
Refused: Runink River step `install` (Run the installer) is destructive. Destructive steps are only ever run by the operator at the console.
```

## Not verified yet

- **Not yet built into an ISO or booted.** The GRUB injection was run against the real
  `artix-grub-live` `kernels.cfg` and produces the new entry, and every script passes
  shellcheck. But the s6 wiring has never run in a live session: the `river-guide-model`
  longrun, the `tty1.conf` GETTY override, and `setsid -c` with `s6-setuidgid` on tty1.
- The device flow and the issue channel have been exercised against a fake GitHub only. A
  real GitHub App client ID with Device Flow enabled, and a real private repository, still
  need a live run. So does NAT64/proxy reachability from an IPv6-only site.
- The model was measured on a laptop CPU, not on the minimum hardware. The 7 GiB and 3 GiB
  thresholds are estimates from measured RSS plus headroom.
- `pacman -Rdd` of `river-guide` in the target chroot is best-effort and unverified.
