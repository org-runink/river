// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package hw

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
)

// installedMemory sums the populated SMBIOS type 17 (Memory Device) structures that the
// kernel exposes as sys/firmware/dmi/entries/17-<n>/raw (root-readable; the tables are the
// firmware's own statement of the RAM that is fitted or soldered). ok is false when there is
// no such entry or none could be decoded.
//
// Layout (SMBIOS 3.x, 7.18): byte 0 type, byte 1 structure length, offset 0x0C Size (WORD):
// 0 = socket empty, 0xFFFF = unknown, 0x7FFF = see Extended Size (DWORD at 0x1C, MB), else
// bit 15 set = KB units, clear = MB units.
func (pr *Prober) installedMemory() (bytes uint64, ok bool) {
	ents, _ := filepath.Glob(pr.p("sys/firmware/dmi/entries/17-*/raw"))
	sort.Strings(ents)
	for _, e := range ents {
		b, err := os.ReadFile(e)
		if err != nil {
			continue
		}
		n, valid := smbiosDeviceBytes(b)
		if !valid {
			continue
		}
		bytes += n
		ok = true
	}
	return bytes, ok && bytes > 0
}

// smbiosDeviceBytes decodes one type 17 structure; valid is false for a structure that is
// too short, not type 17, an empty socket or a size the firmware left unknown.
func smbiosDeviceBytes(b []byte) (uint64, bool) {
	if len(b) < 0x0E || b[0] != 17 || int(b[1]) < 0x0E || int(b[1]) > len(b) {
		return 0, false
	}
	size := binary.LittleEndian.Uint16(b[0x0C:])
	switch size {
	case 0, 0xFFFF:
		return 0, false
	case 0x7FFF:
		if int(b[1]) < 0x20 || len(b) < 0x20 {
			return 0, false
		}
		mb := binary.LittleEndian.Uint32(b[0x1C:]) & 0x7FFFFFFF
		return uint64(mb) << 20, mb > 0
	}
	if size&0x8000 != 0 {
		return uint64(size&0x7FFF) << 10, true
	}
	return uint64(size) << 20, true
}
