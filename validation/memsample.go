// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// memSampler polls a cgroup v2 dir. "Resident" is anon + file_mapped + swapped-out pages: the weights and KV
// cache the process actually holds, whether it read the GGUF into anonymous memory or
// mmapped it. Plain page cache ("file" minus mapped) is reclaimable and is not counted,
// but memory.peak (which does count it) is reported alongside.
type memSampler struct {
	dir     string
	mu      sync.Mutex
	maxRes  int64
	samples int
	done    chan struct{}
	wg      sync.WaitGroup
}

func startSampler(dir string) *memSampler {
	m := &memSampler{dir: dir, done: make(chan struct{})}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			m.sample()
			select {
			case <-m.done:
				return
			case <-t.C:
			}
		}
	}()
	return m
}

func (m *memSampler) sample() {
	st := readKV(filepath.Join(m.dir, "memory.stat"))
	if st == nil {
		return
	}
	res := st["anon"] + st["file_mapped"]
	// Pages the kernel pushed to swap (zram here) are still the process's memory.
	if b, err := os.ReadFile(filepath.Join(m.dir, "memory.swap.current")); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			res += v
		}
	}
	m.mu.Lock()
	if res > m.maxRes {
		m.maxRes = res
	}
	m.samples++
	m.mu.Unlock()
}

func (m *memSampler) stop() map[string]any {
	close(m.done)
	m.wg.Wait()
	out := map[string]any{"samples": m.samples, "cgroup": filepath.Base(m.dir)}
	const gib = 1 << 30
	out["resident_peak_gib"] = round2(float64(m.maxRes) / gib)
	if b, err := os.ReadFile(filepath.Join(m.dir, "memory.peak")); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			out["cgroup_memory_peak_gib"] = round2(float64(v) / gib)
		}
	}
	return out
}

func readKV(p string) map[string]int64 {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]int64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		out[k] = n
	}
	return out
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
