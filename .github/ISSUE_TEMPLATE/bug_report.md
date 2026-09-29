---
name: Bug Report
about: Report something that is broken
title: "[BUG] "
labels: bug, needs-info
assignees: ""
---

## Description
A clear, one-sentence summary of what's broken.

## Steps to Reproduce
1. First, I did this...
2. Then, I did this...
3. Expected X, but got Y

## Expected Behavior
What should have happened? Describe the correct behavior.

## Actual Behavior
What actually happened? Describe the incorrect behavior.

## Environment
- **River version**: (run `cat /etc/runink-os-version`)
- **Hardware**: CPU model, RAM, storage type (NVMe/SATA/HDD)
- **Installation date**: When was River installed?
- **Last boot**: When was the last boot (for timing-related issues)?
- **Relevant software**: Other tools/packages involved?

## Error Messages & Logs

Please include relevant output from:
- `journalctl -u service-name` (if service-related)
- `dmesg` (kernel messages)
- Installation log (if install-related)
- Build log (if build-related)
- Console output (if boot-related)

```
Paste logs here between the backticks.
```

## Possible Root Cause
(Optional) If you have ideas about what might be causing this, describe them here.

## Workaround
(Optional) Any workaround you've found while waiting for a fix.

## Additional Context
Any other information that might help us understand the issue.

---

**Note**: We triage bugs weekly. If we need more information, we'll ask. If we don't hear back within 2 weeks, we may close the issue to keep the backlog manageable. Feel free to reopen with more details.
