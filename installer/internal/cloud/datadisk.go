// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// FindDataDisk returns the block device of the GCE disk attached with device_name name,
// without Google's udev rules (the image does not ship them). In order:
//
//  1. /dev/disk/by-id/google-<name> (Google's rules, if a downstream adds them) and
//     /dev/disk/by-id/scsi-0Google_PersistentDisk_<name> (systemd/eudev's own SCSI rule);
//  2. SCSI (virtio-scsi) disks in sysfs: vendor "Google", model "PersistentDisk", and the
//     device name as the unit serial number (VPD page 0x80);
//  3. NVMe namespaces: GCE writes {"device_name": ...} as JSON into the vendor-specific area
//     of Identify Namespace (bytes 384..4095), which Google's own google_nvme_id reads the
//     same way.
func FindDataDisk(sys, dev, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/ \t\n") {
		return "", fmt.Errorf("bad device name %q", name)
	}
	for _, l := range []string{"google-" + name, "scsi-0Google_PersistentDisk_" + name} {
		if p, err := filepath.EvalSymlinks(filepath.Join(dev, "disk/by-id", l)); err == nil {
			return p, nil
		}
	}
	blocks, _ := filepath.Glob(filepath.Join(sys, "block/*"))
	for _, b := range blocks {
		base := filepath.Base(b)
		switch {
		case strings.HasPrefix(base, "sd"):
			if scsiName(b) == name {
				return filepath.Join(dev, base), nil
			}
		case strings.HasPrefix(base, "nvme"):
			if n, err := nvmeDeviceName(filepath.Join(dev, base), b); err == nil && n == name {
				return filepath.Join(dev, base), nil
			}
		}
	}
	return "", fmt.Errorf("no attached disk with device name %q", name)
}

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// scsiName returns the GCE device name of a SCSI disk, or "" when it is not a GCE PD.
func scsiName(sysBlock string) string {
	if readTrim(filepath.Join(sysBlock, "device/vendor")) != "Google" ||
		readTrim(filepath.Join(sysBlock, "device/model")) != "PersistentDisk" {
		return ""
	}
	return ParseVPD80(mustRead(filepath.Join(sysBlock, "device/vpd_pg80")))
}

func mustRead(p string) []byte { b, _ := os.ReadFile(p); return b }

// ParseVPD80 returns the unit serial number from a VPD page 0x80 (4-byte header, then the
// serial, space padded).
func ParseVPD80(b []byte) string {
	if len(b) < 4 || b[1] != 0x80 {
		return ""
	}
	n := int(b[3])
	if len(b) < 4+n {
		n = len(b) - 4
	}
	return strings.TrimSpace(string(bytes.TrimRight(b[4:4+n], "\x00")))
}

// ParseNVMeVendorName reads the device name from an Identify Namespace data structure.
func ParseNVMeVendorName(id []byte) (string, error) {
	if len(id) < 4096 {
		return "", errors.New("identify namespace: short buffer")
	}
	vs := bytes.TrimRight(id[384:4096], "\x00")
	i := bytes.IndexByte(vs, '{')
	if i < 0 {
		return "", errors.New("identify namespace: no vendor JSON")
	}
	var v struct {
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(bytes.NewReader(vs[i:])).Decode(&v); err != nil || v.DeviceName == "" {
		return "", errors.New("identify namespace: no device_name")
	}
	return v.DeviceName, nil
}

// nvmePassthru is struct nvme_passthru_cmd (linux/nvme_ioctl.h), 72 bytes.
type nvmePassthru struct {
	opcode, flags        uint8
	rsvd1                uint16
	nsid, cdw2, cdw3     uint32
	metadata, addr       uint64
	metadataLen, dataLen uint32
	cdw10, cdw11, cdw12  uint32
	cdw13, cdw14, cdw15  uint32
	timeoutMs, result    uint32
}

// nvmeIoctlAdminCmd is _IOWR('N', 0x41, struct nvme_passthru_cmd).
const nvmeIoctlAdminCmd = 0xC0484E41

func nvmeDeviceName(devPath, sysBlock string) (string, error) {
	nsid, err := strconv.ParseUint(readTrim(filepath.Join(sysBlock, "nsid")), 10, 32)
	if err != nil {
		return "", err
	}
	f, err := os.Open(devPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// The data buffer is an mmap'd page: the kernel writes into it by address, and a Go heap
	// or stack slice could move under a uintptr.
	buf, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		return "", err
	}
	defer syscall.Munmap(buf)
	// #nosec G103 G115 -- the documented NVMe admin ioctl; buf is an mmap'd page (it cannot move), its length is the constant 4096
	cmd := nvmePassthru{opcode: 0x06, nsid: uint32(nsid), addr: uint64(uintptr(unsafe.Pointer(&buf[0]))),
		dataLen: uint32(len(buf)), cdw10: 0, timeoutMs: 5000}
	// #nosec G103 -- passing the command struct to ioctl(2)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), nvmeIoctlAdminCmd, uintptr(unsafe.Pointer(&cmd))); e != 0 {
		return "", e
	}
	return ParseNVMeVendorName(buf)
}

// SecretResource turns a secret id or name into a version resource name:
// "id" -> projects/<project>/secrets/id/versions/latest, and a full name is kept.
func SecretResource(project, s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "projects/"):
		if !ValidSecretName(s) {
			return "", fmt.Errorf("secret %q: want projects/P/secrets/S[/versions/V]", s)
		}
		if !strings.Contains(s, "/versions/") {
			s += "/versions/latest"
		}
		return s, nil
	case project == "":
		return "", fmt.Errorf("secret id %q needs the project id", s)
	}
	r := "projects/" + project + "/secrets/" + s
	if !ValidSecretName(r) {
		return "", fmt.Errorf("secret id %q is not valid", s)
	}
	return r + "/versions/latest", nil
}
