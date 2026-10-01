# Runink River install guide

This guide installs a Runink River server from the Runink River install medium: an s6-init,
ZFS-root Linux appliance whose whole root pool is encrypted at rest. The host is dual-stack
(IPv4 and IPv6); its Kubernetes cluster network is IPv6-only. It is the knowledge `river-guide` answers from, and its steps are the steps
`river-guide` walks you through.

## How river-guide works

`river-guide` starts on the first console (tty1) when you pick **Runink River Sovereignty
Server** at the install medium's boot menu. It answers questions from this guide and cites
the section it used, like [river#how-river-guide-works]. It never invents commands: a
command it shows you is quoted from this guide.

Type a question, or one of these commands at the `river>` prompt:

- `steps` lists every install step and its state; `next` proposes the next one.
- `approve` runs the proposed step when it is a read-only check.
- `done <step>` records that you finished a step yourself; `skip <step>` skips one.
- `hw` shows the hardware summary and `plan` the install plan.
- `remote` controls the optional issue-comment channel (see
  [river#remote-observation-and-questions](40-troubleshooting.md#remote-observation-and-questions)).
- `shell` opens a login shell on this console; `quit` leaves the guide.

Destructive steps (partitioning the disk, creating the pool) are never run by `river-guide`.
It shows the command, and you run it yourself on a second console: press **Alt+F2**, log in,
and run it there.

## Guide model and degraded mode

When the machine has enough memory, the install medium serves a small open-weight language
model on the IPv6 loopback, and `river-guide` uses it to phrase answers from the guide
sections it retrieved. Nothing leaves the machine for this: there is no external AI service.

On a machine with too little memory, or when the medium was built without the model,
`river-guide` runs in degraded mode: it shows the matching guide sections directly. Every
install step works the same way in both modes.

## Platform guide bundles

A downstream platform that runs on Runink River may publish its own installation guide as a
private, signed guide bundle. The install medium carries only the bundle's location and
public verification key, never its content. After you sign in to GitHub (`login`),
`river-guide` fetches the bundle if your GitHub identity can read it, verifies its
signature, and keeps it in memory only. Its steps then follow the Runink River steps. If no
bundle is available, the guide says "no platform guide bundle loaded" and continues with the
Runink River steps alone.
