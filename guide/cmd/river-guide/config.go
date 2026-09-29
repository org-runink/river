// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"io"
	"os"
	"strings"
	"time"
)

type config struct {
	modelURL, modelName string
	noModel             bool
	modelWait           time.Duration
	modelState          string

	auditPath  string
	workDir    string
	sourcesDir string

	hwprobe, plan, planManifest string

	tagIssue, clientID, proxy string
	bundleKey                 string // trust anchor for a platform bundle, supplied at fetch time
	remoteOffer               bool
	remoteApprovals           bool // tests only; the console switches it on with `remote approvals on`
	pollInterval              time.Duration
	devicePoll                time.Duration // tests: device-flow poll interval override

	apiURL, webURL string // tests / GitHub Enterprise
	cmdlinePath    string
}

func parseFlags(args []string, stderr io.Writer) (*config, []string, error) {
	c := &config{}
	fs := flag.NewFlagSet("river-guide", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.modelURL, "model-url", "http://[::1]:8187/v1", "OpenAI-compatible endpoint of the medium's model server (loopback only)")
	fs.StringVar(&c.modelName, "model-name", "", "model id to request (empty: whatever the server loaded)")
	fs.BoolVar(&c.noModel, "no-model", false, "degraded mode: answer with guide sections, no model")
	fs.DurationVar(&c.modelWait, "model-wait", 0, "wait this long for the model before answering a one-shot `ask`")
	fs.StringVar(&c.modelState, "model-state", "/run/river-guide-model/state", "status file written by the model service")
	fs.StringVar(&c.auditPath, "audit-log", "/var/log/river-guide/audit.jsonl", "append-only audit log ('-' = stderr)")
	fs.StringVar(&c.workDir, "work-dir", "/run/river-guide", "tmpfs scratch directory")
	fs.StringVar(&c.sourcesDir, "sources", "/etc/river-guide/sources.d", "directory of private guide-bundle source descriptors")
	fs.StringVar(&c.hwprobe, "hwprobe", "river-hwprobe", "hardware probe binary")
	fs.StringVar(&c.plan, "plan", "river-plan", "install planner binary")
	fs.StringVar(&c.planManifest, "plan-manifest", "/usr/local/share/runink/models.tiers", "model manifest the planner reads")
	fs.StringVar(&c.tagIssue, "tag-issue", "", "install-tracking issue: owner/repo#N, or owner/repo to create one")
	fs.StringVar(&c.clientID, "client-id", "", "GitHub App client ID for the device flow (not a secret)")
	fs.StringVar(&c.proxy, "proxy", "", "HTTPS proxy for GitHub (IPv6-only networks)")
	fs.StringVar(&c.bundleKey, "bundle-key", "", "ed25519:<base64> key a platform guide bundle must be signed with (asked at `login` when unset)")
	fs.BoolVar(&c.remoteOffer, "remote", false, "offer the remote issue channel at start-up")
	fs.BoolVar(&c.remoteApprovals, "remote-approvals", false, "start with remote approvals on (testing; operators use `remote approvals on`)")
	fs.DurationVar(&c.pollInterval, "poll", 20*time.Second, "issue poll interval")
	fs.DurationVar(&c.devicePoll, "device-poll", 0, "override the device-flow poll interval (testing)")
	fs.StringVar(&c.apiURL, "github-api", "https://api.github.com", "GitHub API base URL")
	fs.StringVar(&c.webURL, "github-web", "https://github.com", "GitHub web base URL (device flow)")
	fs.StringVar(&c.cmdlinePath, "cmdline", "/proc/cmdline", "kernel command line to read river.guide.* settings from ('' = none)")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	c.applyCmdline(readCmdline(c.cmdlinePath), set)
	return c, fs.Args(), nil
}

func readCmdline(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, f := range strings.Fields(string(raw)) {
		if !strings.HasPrefix(f, "river.guide") {
			continue
		}
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			v = "1"
		}
		out[k] = v
	}
	return out
}

// applyCmdline fills settings the flags did not set explicitly.
func (c *config) applyCmdline(kv map[string]string, set map[string]bool) {
	if v, ok := kv["river.guide.remote"]; ok && !set["remote"] {
		c.remoteOffer = v == "1" || v == "yes" || v == "on"
	}
	if v := kv["river.guide.issue"]; v != "" && !set["tag-issue"] {
		c.tagIssue = v
	}
	if v := kv["river.guide.client_id"]; v != "" && !set["client-id"] {
		c.clientID = v
	}
	if v := kv["river.guide.proxy"]; v != "" && !set["proxy"] {
		c.proxy = v
	}
	if v := kv["river.guide.bundle_key"]; v != "" && !set["bundle-key"] {
		c.bundleKey = v
	}
	if v, ok := kv["river.guide.model"]; ok && !set["no-model"] && (v == "0" || v == "off" || v == "no") {
		c.noModel = true
	}
}
