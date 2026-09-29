<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Technical Charter (DRAFT) for the Runink River Project

> **DRAFT. Not in force.** This draft follows the structure of the standard LF Projects,
> LLC technical charter, so that the umbrella foundation's counsel can review it quickly.
> The final text is set by the Linux Foundation when the project is accepted. Everything
> in `[brackets]` is a decision still open. Until adoption, [GOVERNANCE.md](GOVERNANCE.md)
> governs the project.

**Adopted:** `[date]`

This Charter sets forth the responsibilities and procedures for technical contribution
to, and oversight of, the **Runink River** open source project (whose pipeline runtime is RIVER,
Raft-Integrated Validated Event Runtime), which has been established as `[Runink River a Series of LF Projects, LLC]` (the
"Project"). LF Projects, LLC ("LF Projects") is a Delaware series limited liability
company. All contributors (including committers, maintainers, and other technical
positions) and other participants in the Project (collectively, "Collaborators") must
comply with the terms of this Charter.

## 1. Mission and scope of the Project

a. The mission of the Project is to build and maintain a **sovereign, reproducible,
   minimal Linux server distribution** (s6 init, ZFS root, IPv6-only cluster network, no runtime
   fetch), together with a **raft-based, validated data-pipeline runtime** (`riverd`) that
   runs on it, suitable for air-gapped and self-hosted deployments.

b. The scope includes: the distribution's kernel and ZFS packaging, the userland base, the
   installer, the image-build tooling, the firewall and sandbox, release engineering and
   supply-chain attestation, the RIVER runtime library and `riverd`, and their
   documentation and tests.

c. **Out of scope:** application products built on Runink River, including any vendor's
   commercial offerings (these ship as optional downstream payloads outside the
   repository), and any component that is not available under the Project's licenses.

## 2. Technical Steering Committee

a. The Technical Steering Committee (the "TSC") will be responsible for all technical
   oversight of the open source Project.

b. At the Project's inception the TSC voting members are the Project's maintainers as
   listed in `MAINTAINERS.md`. After that, the TSC is composed as set out in
   `GOVERNANCE.md`: three to seven members elected by the maintainers for one-year terms,
   with **no more than one third of voting seats (rounded up) held by any single
   employer or group of related companies**. `[Confirm the cap and the inception period.]`

c. The TSC may choose an alternative approach for determining its voting members, and any
   such alternative approach will be documented in `GOVERNANCE.md`. Any meetings of the
   TSC are intended to be open to the public, and can be conducted electronically, via
   teleconference, or in person.

d. TSC projects generally will involve Contributors and Maintainers. The TSC may adopt or
   modify roles so long as the roles are documented in `GOVERNANCE.md`.

e. Participation in the Project through becoming a Contributor and Maintainer is open to
   anyone so long as they abide by the terms of this Charter.

f. The TSC may (1) establish workflow procedures for the submission, approval, and closure
   or archiving of projects, (2) set requirements for the promotion of Contributors to
   Maintainer status, as applicable, and (3) amend, adjust, refine and/or eliminate the
   roles of Contributors and Maintainers, and create new roles, and publicly document any
   TSC roles, as it sees fit.

g. The TSC may elect a TSC Chair, who will preside over meetings of the TSC and will serve
   until their resignation or replacement by the TSC.

h. Responsibilities: the TSC will be responsible for all aspects of oversight relating to
   the Project, which may include: coordinating the technical direction of the Project;
   approving project or system proposals; organizing sub-projects and removing
   sub-projects; creating sub-committees or working groups; appointing representatives to
   work with other open source or open standards communities; establishing community
   norms, workflows, issuing releases, and security issue reporting policies; approving
   and implementing policies and processes for contributing; discussions, seeking
   consensus, and where necessary, voting on technical matters relating to the code base
   that affect multiple projects; and coordinating any marketing, events, or
   communications regarding the Project.

## 3. TSC voting

a. While the Project aims to operate as a consensus-based community, if any TSC decision
   requires a vote to move the Project forward, the voting members of the TSC will vote on
   a one vote per voting member basis.

b. Quorum for TSC meetings requires at least fifty percent of all voting members of the
   TSC to be present. The TSC may continue to meet if quorum is not met but will be
   prevented from making any decisions at the meeting.

c. Except as provided in Section 7.c. and 8.a, decisions by vote at a meeting require a
   majority vote of those in attendance, provided quorum is met. Decisions made by
   electronic vote without a meeting require a majority vote of all voting members of the
   TSC.

d. In the event a vote cannot be resolved by the TSC, any voting member of the TSC may
   refer the matter to the Series Manager for assistance in reaching a resolution.

## 4. Compliance with policies

a. This Charter is subject to the Series Agreement for the Project and the Operating
   Agreement of LF Projects. Contributors will comply with the policies of LF Projects as
   may be adopted and amended by LF Projects, including, without limitation the policies
   listed at https://lfprojects.org/policies/.

b. The TSC may adopt a code of conduct ("CoC") for the Project, which is subject to
   approval by the Series Manager. The Project starts with the Contributor Covenant 2.1
   (`CODE_OF_CONDUCT.md`).

c. When amending or adopting any policy applicable to the Project, LF Projects will
   publish such policy, as to be amended or adopted, on its web site at least 30 days
   prior to such policy taking effect; provided, however, that in the case of any
   amendment of the Trademark Policy or Terms of Use of LF Projects, any such amendment is
   effective upon publication on LF Project's web site.

d. All Collaborators must allow open participation from any individual or organization
   meeting the requirements for contributing under this Charter and any policies adopted
   for all Collaborators by the TSC, regardless of competitive interests. Put another way,
   the Project community must not seek to exclude any participant based on any criteria,
   requirement, or reason other than those that are reasonable and applied on a
   non-discriminatory basis to all Collaborators in the Project community.

e. The Project will operate in a transparent, open, collaborative, and ethical manner at
   all times. The output of all Project discussions, proposals, timelines, decisions, and
   status should be made open and easily visible to all. Any potential violations of this
   requirement should be reported immediately to the Series Manager.

## 5. Community assets

a. LF Projects will hold title to all trade or service marks used by the Project
   ("Project Trademarks"), whether based on common law or registered rights. Project
   Trademarks will be transferred and assigned to LF Projects to hold on behalf of the
   Project. Any use of any Project Trademarks by Collaborators in the Project will be in
   accordance with the license from LF Projects and inure to the benefit of LF Projects.
   **`[Which marks transfer: "Runink River" and the Runink River logo. Whether "Runink" transfers or stays
   with Runink as the vendor mark is an open owner decision. See
   docs/governance/LF-AIDATA.md, owner action 5.]`**

b. The Project will, as permitted and in accordance with such license from LF Projects,
   develop and own all Project GitHub and social media accounts, and domain name
   registrations created by the Project community. `[Project domain to be registered and
   transferred.]`

c. Under no circumstances will LF Projects be expected or required to undertake any action
   on behalf of the Project that is inconsistent with the tax-exempt status or purpose, as
   applicable, of the Joint Development Foundation or LF Projects, LLC.

## 6. General rules and operations

a. The Project will:

   1. engage in the work of the Project in a professional manner consistent with
      maintaining a cohesive community, while also maintaining the goodwill and esteem of
      LF Projects, Joint Development Foundation and other partner organizations in the
      open source community; and
   2. respect the rights of all trademark owners, including any branding and trademark
      usage guidelines.

## 7. Intellectual property policy

a. Collaborators acknowledge that the copyright in all new contributions will be retained
   by the copyright holder as independent works of authorship and that no contributor or
   copyright holder will be required to assign copyrights to the Project.

b. Except as described in Section 7.c., all contributions to the Project are subject to
   the following:

   1. All new inbound code contributions to the Project must be made using the
      **MIT License** (the "Project License"), except contributions to the kernel
      packaging tree (`build/pkgbuilds/runink-kernel/`), which are made under
      **GPL-2.0-only**, and to the OpenZFS package tree (`build/pkgbuilds/runink-zfs/`),
      which follow **CDDL-1.0**. The per-path mapping is in `REUSE.toml` and
      `docs/LICENSING.md`. `[MIT departs from the LF Projects default of Apache-2.0; LF
      Projects accepts OSI-approved alternatives, to be confirmed with the Series
      Manager.]`
   2. All new inbound code contributions must also be accompanied by a **Developer
      Certificate of Origin** (http://developercertificate.org) sign-off in the source
      code system that is submitted through a TSC-approved contribution process which will
      bind the authorized contributor and, if not self-employed, their employer to the
      applicable license.
   3. All outbound code will be made available under the Project License, or under the
      tree-specific license named in 7.b.1.
   4. Documentation will be received and made available by the Project under the
      **MIT License**.
   5. The Project may seek to integrate and contribute back to other open source projects
      ("Upstream Projects"). In such cases, the Project will conform to all license
      requirements of the Upstream Projects, including dependencies, leveraged by the
      Project. Upstream Project code contributions not stated otherwise in this Charter
      should be submitted to the Upstream Project under that Upstream Project's license.
      **For Runink River this covers, at least: Linux kernel packaging and patches
      (GPL-2.0-only), OpenZFS (CDDL-1.0, shipped as a separate, out-of-tree module
      package and never merged into the kernel source; see
      `docs/governance/ZFS-LICENSING.md`), and the archzfs initcpio hook (MIT).**

c. The TSC may approve the use of an alternative license or licenses for inbound or
   outbound contributions on an exception basis. To request an exception, please describe
   the contribution, the alternative open source license(s), and the justification for
   using an alternative open source license for the Project. License exceptions must be
   approved by a two-thirds vote of the entire TSC.

d. Contributed files should contain license information, such as SPDX short form
   identifiers, indicating the open source license or licenses pertaining to the file.
   Runink River follows the REUSE specification.

## 8. Amendments

a. This Charter may be amended by a two-thirds vote of the entire TSC and is subject to
   approval by LF Projects.
