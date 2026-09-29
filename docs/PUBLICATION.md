<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Publishing Runink River

The owner's step-by-step for announcing Runink River as an open-source project (the LF AI &
Data proposal, [governance/lfaidata-proposal.md](governance/lfaidata-proposal.md)). Every
step here is an owner action: agents prepare pull requests, they never change repository
settings, delete releases, create repositories or push a published history.

The publication rules this page serves (AGENTS.md, "Public repository"; restated, never
weakened):

- The public tree carries **no** downstream product name (only "Runink River"), no internal
  address, host name or runner label, no private repository name and no customer data.
- Prose says "Runink River", never a bare "River"; identifiers stay as they are.
- Licences: MIT userspace, GPL-2.0-only kernel tree, CDDL-1.0 OpenZFS tree, CC-BY-4.0
  artwork, `LicenseRef-Runink-Trademark` for the marks; the mascot is all-rights-reserved
  brand art ([LICENSING.md](LICENSING.md)).
- Every commit carries a DCO sign-off, by a person: maintainers commit and sign off as
  themselves (the owner as `Daniel Paes <78096758+paesdan@users.noreply.github.com>`, the
  GitHub no-reply address, never a personal mailbox), and a bot identity never authors or
  signs off a commit.
- Publication is a fresh single-commit history pushed by the owner, never a visibility flip
  of a repository with private history.

## Where things stand (2026-09-28)

- `org-runink/river` is **already public**, with its full development history: 274 commits on
  `main`, and 478 reachable from all refs, which include one tag and 162 `refs/pull/*` refs
  that GitHub keeps and serves even after a force-push. It has 0 forks and 1 star.
- `river lint public-leak --history` over a mirror clone (every ref, including the pull
  refs) finds leaks that no commit to the tree can remove:

  | Category | Where in the history | Commits |
  |---|---|---|
  | Excerpt of a proprietary application's source | a `build/cross-repo/` patch, added 2026-07-30, deleted 2026-09-24 | 8 |
  | Downstream product names | docs, the former server profile, commit messages; and 130 commits whose **author identity** is a bot named after a product | 159 |
  | Downstream product host names, a LAN domain | the former server profile's host list and smoke test, a deployment write-up | 23 |
  | Private repository names | workflows, docs, commit messages | 21 |
  | Private-range addresses of the owner's network, tunnel-broker IPv6 prefixes | a deployment write-up, the former server profile, commit messages | 24 |
  | The public IPv4 address of a residential line, and tunnel endpoints | a deployment write-up and three server-profile files (found by a manual scan; the lint does not look for public addresses) | 9 |
  | Self-hosted runner labels | workflows, CI docs | 17 |
  | The downstream server image's name | profile paths, scripts, messages | 61 |
  | A personal e-mail address (not the maintainer's) | a `.mailmap` added 2026-09-03, and two review docs quoting it | 3 |
  | A company bot address as author and sign-off | commit identities and trailers | 293 |

  gitleaks (every ref) finds **no credential**: its 20 hits are key fingerprints, SSH key
  type names and a test constant.
- The latest GitHub release is **`runink-os-2026.07`**, titled with the server image's name: an old
  server ISO whose notes name a second inference engine and a private repository's install
  command, with an unsigned `.sha256` and **no `SHA256SUMS.asc`**. `install.sh` downloads
  from `releases/latest`, and against this release it **fails closed**: the first request,
  `releases/latest/download/SHA256SUMS`, answers 404 and the script exits 1 with "download
  failed" before downloading any image (checked with `--dry-run` on 2026-09-28).
- The settings the project documents are not in place yet: no branch protection or ruleset
  on `main`, private vulnerability reporting off, fork pull requests need approval only from
  first-time contributors, Discussions off, the wiki on.
- The documentation is live at <https://docs.runink.org/river/>, served by GitHub Pages of
  the `org-runink/docs` repository (its `gh-pages` branch, custom domain `docs.runink.org`);
  Pages is not enabled on this repository.

## The decision: two paths

**Path A: keep this repository and accept its history.** Delete the old release, turn on the
settings, announce. Nothing is re-created, and links, the star and the issue and PR history
stay. The leaks above stay public for good: a rewrite in place (`git filter-repo` plus a
force-push) does not remove them, because GitHub keeps serving every `refs/pull/*` and the
commits they point to, and forks and caches keep their copies.

**Path B (recommended): republish from a clean snapshot, and take this repository out of
public view.** Rename this repository and make it private (it becomes the archive the
licensing docs describe), then create a new public `org-runink/river` holding one DCO-signed
commit. Because the new repository takes the same name, `install.sh`'s URL, the badges, the
docs links and every `org-runink/river` reference keep working. What is lost: the one star,
the issue and pull-request history, and the old release. What it cannot undo: anyone who
already cloned or archived the public history keeps it (check Software Heritage,
<https://archive.softwareheritage.org/>, for an origin of this repository and ask for removal
if it is there).

**Recommendation: Path B.** The history holds an excerpt of proprietary application code,
the product names the publication rules exclude, the owner's network addresses and a third
party's personal e-mail address. Only Path B takes them off the project's public face, and
with 0 forks and 1 star it costs almost nothing now; it costs more every day after the
announcement.

Whichever path: do step 1 first, today.

## 1. Remove the old release (both paths, now)

Recommended, deleting the release and its tag:

```bash
gh release delete runink-os-2026.07 -R org-runink/river --cleanup-tag --yes
```

The alternative, if the owner wants to keep it visible: demote it to a pre-release that is no
longer "latest", with a warning at the top of its notes.

```bash
gh release view runink-os-2026.07 -R org-runink/river --json body --jq .body > ~/.cache/river-publish-old-notes.md
# edit the file: remove the private install command and the engine name, and put this first:
#   > **Superseded. Do not install.** This is an unsigned pre-release image kept for the
#   > record; install Runink River with install.sh from a signed release.
gh release edit runink-os-2026.07 -R org-runink/river --prerelease --latest=false \
  --title "runink-os-2026.07 (superseded, unsigned)" --notes-file ~/.cache/river-publish-old-notes.md
```

Either way, `releases/latest` then has nothing until the first signed release, and
`install.sh` keeps failing closed with "download failed" (404) until then.

## 2. Verify the tree (both paths)

After the pull requests you want in the first public commit are merged:

```bash
git clone https://github.com/org-runink/river.git ~/.cache/river-publish/verify
cd ~/.cache/river-publish/verify
C=$(git rev-parse HEAD); echo "publishing $C"      # write the commit down
sh scripts/ci-tier1.sh                              # must end: ci-tier1: OK
(cd cli && go build -o ~/.cache/river-publish/river ./cmd/river)
~/.cache/river-publish/river lint reuse --repo .        # REUSE 3.3
~/.cache/river-publish/river lint public-leak --repo .  # the leak scan (also in Tier 1)
gitleaks dir --no-banner --redact .                     # secrets in the tree
```

Tier 1 runs every lint, the Go tests and `river lint public-leak`; running the two lints on
their own shows their output. On 2026-09-28 `gitleaks dir` reported 8 findings in the tree,
all false positives of its `generic-api-key` rule: OpenPGP fingerprints (`install.sh`, the AUR
`.SRCINFO` files), a cloud metadata key name and a test constant. Anything else is a finding. Read the diff of `scripts/public-leak.allow` since the last
review: every entry is an exception someone argued for.

## 3a. Path B: publish a fresh single-commit history

```bash
# The snapshot: the tracked files at $C, nothing else (git archive leaves .git behind).
mkdir -p ~/.cache/river-publish/snapshot
git -C ~/.cache/river-publish/verify archive --format=tar "$C" | tar -x -C ~/.cache/river-publish/snapshot
cd ~/.cache/river-publish/snapshot
git init -q -b main
git add -A
# Commit with the identity you want public (the GitHub no-reply address keeps a mailbox out
# of the history), signed (-S) and signed off (-s).
git -c user.name="Daniel Paes" -c user.email="78096758+paesdan@users.noreply.github.com" \
  commit -q -S -s -m "Runink River: initial public import" \
  -m "The Runink River tree at commit $C of the development repository, which is kept as a private archive."

# Check the new history exactly as the lint will see it: one commit, no leak.
~/.cache/river-publish/river lint public-leak --history --repo .
~/.cache/river-publish/river lint public-leak --repo .
~/.cache/river-publish/river lint reuse --repo .
git log --format='%an <%ae>%n%B' ; git verify-commit HEAD

# Take the old repository out of public view. Renaming first frees the name; making it
# private hides the history, the pull refs and the old release. (GitHub warns that stars and
# watchers are dropped when a public repository goes private.)
gh repo rename river-history -R org-runink/river --yes
gh repo edit org-runink/river-history --visibility private --accept-visibility-change-consequences

# The new public repository, under the old name.
gh repo create org-runink/river --public \
  --description "Runink River: an optimized developer workstation on s6" \
  --homepage https://docs.runink.org/river/ --disable-wiki
git remote add origin https://github.com/org-runink/river.git
git push -u origin main
```

Then check that `https://github.com/org-runink/river` shows one commit, that
`https://raw.githubusercontent.com/org-runink/river/main/install.sh` serves the script, and
that the first CI run on `main` is green.

## 3b. Path A: keep the history

Nothing to push. Record the decision (a comment on the proposal issue, and a line in
[governance/LF-AIDATA.md](governance/LF-AIDATA.md), owner action 2), then go on to step 4.

## 4. Repository settings (both paths)

Fork pull requests run only after a maintainer approves them:

```bash
gh api -X PUT repos/org-runink/river/actions/permissions/fork-pr-contributor-approval \
  -f approval_policy=all_external_contributors
```

Private vulnerability reporting (the second channel in [SECURITY.md](../SECURITY.md)),
Discussions, no wiki, Dependabot security updates, secret scanning with push protection:

```bash
gh api -X PUT repos/org-runink/river/private-vulnerability-reporting
gh repo edit org-runink/river --enable-discussions --enable-wiki=false
gh api -X PUT repos/org-runink/river/automated-security-fixes
gh api -X PATCH repos/org-runink/river --input - <<'EOF'
{"security_and_analysis": {"secret_scanning": {"status": "enabled"},
                           "secret_scanning_push_protection": {"status": "enabled"}}}
EOF
```

Branch protection on `main`: the pull-request checks CI runs, DCO included, a linear history,
no force-push or deletion. With one maintainer, a required approving review would block every
merge, so the count is 0 until a second maintainer joins (then set it to 1,
[GOVERNANCE.md](../GOVERNANCE.md)). Check the context names against the first CI run's check
names before applying.

```bash
gh api -X PUT repos/org-runink/river/branches/main/protection --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "tier1 (lint, rootless podman)",
      "installer Go (river-hwprobe, river-plan)",
      "river-guide (vet + test)",
      "Go security analysis (gosec, govulncheck)",
      "REUSE lint",
      "DCO sign-off"
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {"required_approving_review_count": 0, "dismiss_stale_reviews": true},
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "required_conversation_resolution": true
}
EOF
```

Also:

- **Pages.** The documentation is served today from the `org-runink/docs` repository at
  <https://docs.runink.org/river/>. Keep one source: either leave that as it is (and leave
  Pages off here, so `docs.yml` only builds), or move publishing to this repository with
  `gh api -X POST repos/org-runink/river/pages -f build_type=workflow` and retire the copy
  there. Do not run both.
- **Scorecard.** `scorecard.yml` works with the default token on a public repository; the
  `SCORECARD_TOKEN` secret is optional.
- **DCO app and 2FA** are organisation settings (LF-AIDATA owner action 3).
- **Registrations that need the public URL:** the OpenSSF Best Practices badge
  (<https://www.bestpractices.dev/>) and the REUSE API (<https://api.reuse.software/register>);
  uncomment their badges in the README ([governance/BADGES.md](governance/BADGES.md)).

## 5. The first signed release

Follow [RELEASE.md](../RELEASE.md); in commands (`YYYY.MM` is the release month):

```bash
# a. A pull request that sets VERSION and moves CHANGELOG's [Unreleased] to the release; merge it.
# b. Build and test the ISO from the merged commit (about 40 GB free under ~/.cache).
git clone https://github.com/org-runink/river.git ~/.cache/river-release && cd ~/.cache/river-release
build/local-iso.sh
sudo sh build/iso-root-stage.sh ~/.cache/river-build/local-iso/root-stage-river.env
build/qemu-gui-test.sh ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso   # every RIVERTEST OK
# c. Assets, then the offline signature over SHA256SUMS with the key in KEYS.
build/release-assets.sh ~/.cache/river-release-out ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso
gpg --local-user 95C0A7B97D547413E42660DDB06FE75626F15BF3 --armor --detach-sign ~/.cache/river-release-out/SHA256SUMS
gpg --verify ~/.cache/river-release-out/SHA256SUMS.asc ~/.cache/river-release-out/SHA256SUMS
# d. Signed tag, DRAFT release, then the gate: dry run first, then for real.
git tag -s runink-os-YYYY.MM -m "Runink River runink-os-YYYY.MM" && git push origin runink-os-YYYY.MM
gh release create runink-os-YYYY.MM --draft --title "Runink River runink-os-YYYY.MM" \
  --notes-file <the CHANGELOG section> ~/.cache/river-release-out/*
gh workflow run release-gate.yml -f tag=runink-os-YYYY.MM -f dry_run=true
gh workflow run release-gate.yml -f tag=runink-os-YYYY.MM -f dry_run=false
```

A pushed `runink-os-*` tag also starts `images.yml` (a hosted-runner ISO build that has not
yet completed a run); it publishes nothing. Once the gate publishes the release, check the
one-line install end to end on a spare stick:

```bash
curl -fsSL https://raw.githubusercontent.com/org-runink/river/main/install.sh | sh -s -- --dry-run
```

## 6. Announce

- Update [governance/LF-AIDATA.md](governance/LF-AIDATA.md) (owner actions 2, 7 and 15) and
  the proposal's "Source control" section.
- Open the proposal pull request on `lfai/proposing-projects` (owner action 9) once the
  sponsor is found.
- Announce on runink.org with the one-line install, the docs link
  (<https://docs.runink.org/river/>) and the release's verification steps.
- Re-run step 2 and `river lint public-leak --history --repo .` on the public repository
  after the first week of outside contributions.
