// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package cloud

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Partition describes the partition that holds the root pool, read from sysfs.
type Partition struct {
	Dev       string // /dev/nvme0n1p2
	Disk      string // /dev/nvme0n1
	Number    int    // 2
	Start     int64  // first sector (512-byte units, as sysfs reports)
	Sectors   int64  // length in 512-byte sectors
	DiskTotal int64  // disk length in 512-byte sectors
}

// ReadPartition resolves a partition device (e.g. /dev/sda2) through sysfs rooted at sys
// (normally "/sys").
func ReadPartition(sys, dev string) (*Partition, error) {
	real, err := filepath.EvalSymlinks(dev)
	if err != nil {
		real = dev
	}
	name := filepath.Base(real)
	pdir := filepath.Join(sys, "class/block", name)
	num, err := readInt(filepath.Join(pdir, "partition"))
	if err != nil {
		return nil, fmt.Errorf("%s is not a partition: %w", dev, err)
	}
	start, err := readInt(filepath.Join(pdir, "start"))
	if err != nil {
		return nil, err
	}
	size, err := readInt(filepath.Join(pdir, "size"))
	if err != nil {
		return nil, err
	}
	link, err := filepath.EvalSymlinks(pdir)
	if err != nil {
		return nil, err
	}
	diskName := filepath.Base(filepath.Dir(link))
	total, err := readInt(filepath.Join(sys, "class/block", diskName, "size"))
	if err != nil {
		return nil, err
	}
	return &Partition{Dev: "/dev/" + name, Disk: "/dev/" + diskName, Number: int(num),
		Start: start, Sectors: size, DiskTotal: total}, nil
}

func readInt(p string) (int64, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// GrowSlack is how much free space at the end of the disk is not worth a resize (1 GiB):
// the GPT backup header and alignment always leave a little.
const GrowSlack int64 = 1 << 30

// NeedsGrow reports whether the partition should be extended to the end of its disk. The
// last 34 sectors hold the backup GPT; the partition may end no later than before them.
func (p *Partition) NeedsGrow() bool {
	usableEnd := p.DiskTotal - 34
	end := p.Start + p.Sectors
	return (usableEnd-end)*512 > GrowSlack
}

// SgdiskInfo is what `sgdisk -i N DISK` reports about one partition, needed to recreate it
// with a new end and nothing else changed.
type SgdiskInfo struct {
	TypeGUID   string
	UniqueGUID string
	FirstLBA   int64
	Name       string
}

// ParseSgdiskInfo reads `sgdisk -i N DISK` output.
func ParseSgdiskInfo(out string) (*SgdiskInfo, error) {
	var in SgdiskInfo
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Partition GUID code":
			in.TypeGUID, _, _ = strings.Cut(v, " ")
		case "Partition unique GUID":
			in.UniqueGUID = v
		case "First sector":
			f, _, _ := strings.Cut(v, " ")
			n, err := strconv.ParseInt(f, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("sgdisk: first sector %q", v)
			}
			in.FirstLBA = n
		case "Partition name":
			in.Name = strings.Trim(v, "'")
		}
	}
	if in.TypeGUID == "" || in.UniqueGUID == "" || in.FirstLBA == 0 {
		return nil, fmt.Errorf("sgdisk -i: incomplete output")
	}
	return &in, nil
}

// SgdiskGrowArgs recreates partition n from the same first sector to the end of the disk,
// keeping its type, unique GUID and name. The data on it is untouched: GPT only records
// where the partition begins and ends.
func SgdiskGrowArgs(disk string, n int, in *SgdiskInfo) []string {
	args := []string{
		"-d", strconv.Itoa(n),
		"-n", fmt.Sprintf("%d:%d:0", n, in.FirstLBA),
		"-t", fmt.Sprintf("%d:%s", n, in.TypeGUID),
		"-u", fmt.Sprintf("%d:%s", n, in.UniqueGUID),
	}
	if in.Name != "" {
		args = append(args, "-c", fmt.Sprintf("%d:%s", n, in.Name))
	}
	return append(args, disk)
}
