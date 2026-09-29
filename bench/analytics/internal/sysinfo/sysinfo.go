// Package sysinfo records what a benchmark result is a result OF: the running kernel, its
// command line and a hash of its build configuration, the kernel's runtime policy knobs that
// the analytics profile changes, and the CPU and memory. Two results are only comparable when
// everything here except the kernel is the same.
//
// It reads /proc and /sys only; nothing requires root. A value that cannot be read is
// recorded as "" (or 0), never guessed.
package sysinfo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Info is the host and kernel description stored in every results file.
type Info struct {
	KernelRelease string `json:"kernel_release"`
	KernelVersion string `json:"kernel_version"`
	Cmdline       string `json:"cmdline"`
	// ConfigSHA256 hashes the kernel's build configuration; ConfigSource says where it was
	// read from (/proc/config.gz, /boot/config-<rel>, or the module build directory).
	ConfigSHA256 string `json:"config_sha256"`
	ConfigSource string `json:"config_source"`
	// Selected CONFIG_ values, read from that same configuration.
	Config map[string]string `json:"config"`
	// Runtime policy the analytics profile sets or leaves to the operator.
	Runtime map[string]string `json:"runtime"`

	CPUModel      string `json:"cpu_model"`
	LogicalCPUs   int    `json:"logical_cpus"`
	PhysicalCores int    `json:"physical_cores"`
	NUMANodes     int    `json:"numa_nodes"`
	MemTotalKiB   int64  `json:"mem_total_kib"`
	GoVersion     string `json:"go_version"`
}

// configKeys are the CONFIG_ symbols the report shows, because the analytics profile sets
// them (see docs/KERNEL.md).
var configKeys = []string{
	"CONFIG_HZ", "CONFIG_PREEMPT", "CONFIG_PREEMPT_LAZY", "CONFIG_PREEMPT_DYNAMIC",
	"CONFIG_ZEN_INTERACTIVE", "CONFIG_TRANSPARENT_HUGEPAGE_ALWAYS",
	"CONFIG_TRANSPARENT_HUGEPAGE_MADVISE", "CONFIG_DEFAULT_TCP_CONG", "CONFIG_DEFAULT_NET_SCH",
	"CONFIG_SCHED_AUTOGROUP", "CONFIG_WQ_POWER_EFFICIENT_DEFAULT", "CONFIG_SCHED_CLASS_EXT",
	"CONFIG_LRU_GEN_ENABLED", "CONFIG_NUMA_BALANCING_DEFAULT_ENABLED", "CONFIG_ZSWAP_DEFAULT_ON",
}

// runtimeFiles maps a report key to the /proc or /sys file holding the live value.
var runtimeFiles = map[string]string{
	"thp_enabled":          "/sys/kernel/mm/transparent_hugepage/enabled",
	"thp_defrag":           "/sys/kernel/mm/transparent_hugepage/defrag",
	"tcp_congestion":       "/proc/sys/net/ipv4/tcp_congestion_control",
	"default_qdisc":        "/proc/sys/net/core/default_qdisc",
	"numa_balancing":       "/proc/sys/kernel/numa_balancing",
	"sched_autogroup":      "/proc/sys/kernel/sched_autogroup_enabled",
	"mglru_enabled":        "/sys/kernel/mm/lru_gen/enabled",
	"zswap_enabled":        "/sys/module/zswap/parameters/enabled",
	"swappiness":           "/proc/sys/vm/swappiness",
	"cpufreq_governor":     "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor",
	"energy_perf_pref":     "/sys/devices/system/cpu/cpu0/cpufreq/energy_performance_preference",
	"io_uring_disabled":    "/proc/sys/kernel/io_uring_disabled",
	"preempt_dynamic_mode": "/sys/kernel/debug/sched/preempt", // root only; "" otherwise
}

// Collect gathers Info for the running system.
func Collect() Info {
	var in Info
	var u syscall.Utsname
	if syscall.Uname(&u) == nil {
		in.KernelRelease = utsString(u.Release[:])
		in.KernelVersion = utsString(u.Version[:])
	}
	in.Cmdline = RedactCmdline(readTrim("/proc/cmdline"))
	cfg, src := readKernelConfig(in.KernelRelease)
	if cfg != nil {
		sum := sha256.Sum256(cfg)
		in.ConfigSHA256 = hex.EncodeToString(sum[:])
		in.ConfigSource = src
		in.Config = pickConfig(cfg, configKeys)
	}
	in.Runtime = map[string]string{}
	for k, f := range runtimeFiles {
		in.Runtime[k] = readTrim(f)
	}
	in.Runtime["mitigations"] = mitigations()
	in.CPUModel, in.PhysicalCores = cpuInfo()
	in.LogicalCPUs = runtime.NumCPU()
	in.NUMANodes = countGlob("/sys/devices/system/node/node[0-9]*")
	in.MemTotalKiB = memTotal()
	in.GoVersion = runtime.Version()
	return in
}

func utsString(b []int8) string {
	var sb strings.Builder
	for _, c := range b {
		if c == 0 {
			break
		}
		sb.WriteByte(byte(c))
	}
	return sb.String()
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readKernelConfig returns the running kernel's config and where it came from. Arch kernels
// (linux, linux-lts, linux-zen, linux-runink) all build IKCONFIG_PROC, so /proc/config.gz is
// the normal source.
func readKernelConfig(release string) ([]byte, string) {
	if f, err := os.Open("/proc/config.gz"); err == nil {
		defer f.Close()
		if zr, err := gzip.NewReader(f); err == nil {
			if b, err := io.ReadAll(zr); err == nil {
				return b, "/proc/config.gz"
			}
		}
	}
	for _, p := range []string{
		"/boot/config-" + release,
		filepath.Join("/usr/lib/modules", release, "build/.config"),
	} {
		if b, err := os.ReadFile(p); err == nil {
			return b, p
		}
	}
	return nil, ""
}

// pickConfig extracts keys from a kernel .config. A key that is "# ... is not set" or absent
// is reported as "n".
func pickConfig(cfg []byte, keys []string) map[string]string {
	vals := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(cfg))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if k, v, ok := strings.Cut(line, "="); ok && strings.HasPrefix(k, "CONFIG_") {
			vals[k] = strings.Trim(v, `"`)
		}
	}
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := vals[k]; ok {
			out[k] = v
		} else {
			out[k] = "n"
		}
	}
	return out
}

// reUUID matches filesystem and LUKS UUIDs, which identify the machine's disks and have no
// bearing on a kernel comparison. Results files are meant to be shared, so they are redacted.
var reUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

// RedactCmdline replaces every UUID in a kernel command line with "<uuid>".
func RedactCmdline(s string) string { return reUUID.ReplaceAllString(s, "<uuid>") }

var reModel = regexp.MustCompile(`^model name\s*:\s*(.+)$`)

// cpuInfo returns the CPU model and the number of distinct physical cores
// (physical id, core id pairs).
func cpuInfo() (string, int) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", 0
	}
	defer f.Close()
	model := ""
	cores := map[string]bool{}
	phys, core := "", ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := reModel.FindStringSubmatch(line); m != nil && model == "" {
			model = strings.TrimSpace(m[1])
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			if phys != "" || core != "" {
				cores[phys+"/"+core] = true
			}
			phys, core = "", ""
			continue
		}
		switch strings.TrimSpace(k) {
		case "physical id":
			phys = strings.TrimSpace(v)
		case "core id":
			core = strings.TrimSpace(v)
		}
	}
	if phys != "" || core != "" {
		cores[phys+"/"+core] = true
	}
	return model, len(cores)
}

func memTotal() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			n, _ := strconv.ParseInt(fields[1], 10, 64)
			return n
		}
	}
	return 0
}

func countGlob(pattern string) int {
	m, _ := filepath.Glob(pattern)
	return len(m)
}

// mitigations summarises /sys/devices/system/cpu/vulnerabilities as "name=status;..." so a
// result taken with mitigations off can never pass for one taken with them on.
func mitigations() string {
	files, _ := filepath.Glob("/sys/devices/system/cpu/vulnerabilities/*")
	sort.Strings(files)
	parts := make([]string, 0, len(files))
	for _, f := range files {
		v := readTrim(f)
		if i := strings.IndexAny(v, ";,("); i > 0 {
			v = strings.TrimSpace(v[:i])
		}
		parts = append(parts, filepath.Base(f)+"="+v)
	}
	return strings.Join(parts, ";")
}
