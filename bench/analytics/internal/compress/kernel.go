package compress

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// The kernel layer: the kernel image, its loadable modules, and an initramfs, each under
// the codecs the kernel can decompress (CONFIG_KERNEL_*, CONFIG_MODULE_COMPRESS_*,
// CONFIG_RD_*). Inputs are the built linux-runink and runink-zfs packages, read-only.

// WriteNewc writes files as a cpio "newc" archive, the initramfs format
// (Documentation/driver-api/early-userspace/buffer-format.rst).
func WriteNewc(w io.Writer, files []File) error {
	pad := func(n int) []byte { return make([]byte, (4-n%4)%4) }
	write := func(ino int, name string, mode uint32, data []byte) error {
		hdr := fmt.Sprintf("070701%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X",
			ino, mode, 0, 0, 1, 0, len(data), 0, 0, 0, 0, len(name)+1, 0)
		var b bytes.Buffer
		b.WriteString(hdr)
		b.WriteString(name)
		b.WriteByte(0)
		b.Write(pad(len(hdr) + len(name) + 1))
		b.Write(data)
		b.Write(pad(len(data)))
		_, err := w.Write(b.Bytes())
		return err
	}
	for i, f := range files {
		if err := write(i+1, strings.TrimPrefix(f.Name, "/"), 0o100644, f.Data); err != nil {
			return err
		}
	}
	return write(0, "TRAILER!!!", 0, nil)
}

// initramfsModules are the modules a ZFS-root laptop's autodetected initramfs carries
// (storage, USB boot, input for the passphrase, KMS for the splash) on an AMD machine:
// autodetect keeps the one GPU driver in use.
var initramfsModules = []string{"nvme.ko", "nvme-core.ko", "ahci.ko", "libahci.ko", "xhci-pci.ko", "xhci-hcd.ko",
	"usb-storage.ko", "uas.ko", "usbhid.ko", "hid-generic.ko", "amdgpu.ko", "spl.ko", "zfs.ko"}

// initramfsHostFiles are the userland an initramfs brings (the dynamic loader, libc and
// the libraries the ZFS tools and udev load), taken from this host.
var initramfsHostFiles = []string{"/usr/lib/ld-linux-x86-64.so.2", "/usr/lib/libc.so.6", "/usr/lib/libm.so.6",
	"/usr/lib/libblkid.so.1", "/usr/lib/libudev.so.1", "/usr/lib/libuuid.so.1", "/usr/lib/libcrypto.so.3",
	"/usr/lib/libz.so.1", "/usr/lib/libzstd.so.1", "/usr/lib/libkmod.so.2", "/usr/bin/kmod", "/usr/bin/udevadm", "/usr/bin/bash"}

func extract(pkg, dir string, members ...string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	args := append([]string{"-xf", pkg, "-C", dir, "--wildcards"}, members...)
	if out, err := exec.Command("tar", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tar %s: %w: %s", filepath.Base(pkg), err, bytes.TrimSpace(out))
	}
	return nil
}

// vmlinuxFromBzImage returns the uncompressed kernel carried in a zstd-compressed bzImage.
func vmlinuxFromBzImage(t Tools, bz []byte) ([]byte, error) {
	i := bytes.Index(bz, []byte{0x28, 0xb5, 0x2f, 0xfd})
	if i < 0 {
		return nil, fmt.Errorf("vmlinuz: no zstd payload (kernel not built with CONFIG_KERNEL_ZSTD?)")
	}
	cmd := exec.Command(t.Zstd, "-dc", "-q")
	cmd.Stdin = bytes.NewReader(bz[i:])
	out, _ := cmd.Output() // the size bytes after the frame make zstd exit non-zero
	if !bytes.HasPrefix(out, []byte("\x7fELF")) {
		return nil, fmt.Errorf("vmlinuz: payload did not decompress to an ELF image")
	}
	return out, nil
}

// capBytes keeps the first n bytes (n <= 0: all); smoke runs use it to keep the kernel
// layer short.
func capBytes(b []byte, n int) []byte {
	if n > 0 && len(b) > n {
		return b[:n]
	}
	return b
}

// KernelInputs are the kernel-layer inputs extracted from the packages.
type KernelInputs struct {
	Vmlinux        string // path
	InitramfsRaw   string // cpio, modules uncompressed inside (MODULES_DECOMPRESS=yes)
	InitramfsKoZst string // cpio, modules kept .ko.zst inside
	Modules        []File // sampled modules, uncompressed
	ModulesAsBuilt int64  // their .ko.zst bytes as the package ships them
	Release        string
}

// PrepareKernel extracts the kernel layer inputs from the packages into o.Work/kernel.
func PrepareKernel(o Options) (*KernelInputs, error) {
	if o.KernelPkg == "" {
		return nil, fmt.Errorf("no kernel package (pass -kernel-pkg linux-runink-*.pkg.tar.zst)")
	}
	dir := filepath.Join(o.Work, "kernel")
	_ = os.RemoveAll(dir)
	if err := extract(o.KernelPkg, dir, "usr/lib/modules/*"); err != nil {
		return nil, err
	}
	if o.ZFSPkg != "" {
		if err := extract(o.ZFSPkg, dir, "usr/lib/modules/*"); err != nil {
			return nil, err
		}
	}
	vz, _ := filepath.Glob(filepath.Join(dir, "usr/lib/modules/*/vmlinuz"))
	if len(vz) != 1 {
		return nil, fmt.Errorf("kernel package: want one vmlinuz, found %d", len(vz))
	}
	in := &KernelInputs{Release: filepath.Base(filepath.Dir(vz[0]))}
	bz, err := os.ReadFile(vz[0])
	if err != nil {
		return nil, err
	}
	vmlinux, err := vmlinuxFromBzImage(o.Tools, bz)
	if err != nil {
		return nil, err
	}
	in.Vmlinux = filepath.Join(dir, "vmlinux")
	if err := os.WriteFile(in.Vmlinux, capBytes(vmlinux, o.StreamCap), 0o600); err != nil {
		return nil, err
	}

	var kos []string
	_ = filepath.Walk(filepath.Join(dir, "usr/lib/modules"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() && strings.HasSuffix(p, ".ko.zst") {
			kos = append(kos, p)
		}
		return nil
	})
	sort.Strings(kos)
	if len(kos) == 0 {
		return nil, fmt.Errorf("kernel package: no .ko.zst modules")
	}
	decomp := func(p string) ([]byte, error) {
		return exec.Command(o.Tools.Zstd, "-dc", "-q", p).Output()
	}
	byBase := map[string]string{}
	for _, p := range kos {
		byBase[strings.TrimSuffix(filepath.Base(p), ".zst")] = p
	}
	// Sample: o.ModuleCount modules spread evenly over the tree in path order.
	stride := max(1, len(kos)/max(1, o.ModuleCount))
	for i := 0; i < len(kos) && len(in.Modules) < o.ModuleCount; i += stride {
		b, err := decomp(kos[i])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", kos[i], err)
		}
		st, _ := os.Stat(kos[i])
		in.ModulesAsBuilt += st.Size()
		in.Modules = append(in.Modules, File{Name: strings.TrimPrefix(kos[i], dir), Data: b})
	}

	// The initramfs, twice: modules uncompressed inside, and as .ko.zst.
	var raw, kz []File
	for _, m := range initramfsModules {
		p, ok := byBase[m]
		if !ok {
			continue
		}
		b, err := decomp(p)
		if err != nil {
			return nil, err
		}
		z, _ := os.ReadFile(p)
		name := strings.TrimPrefix(p, dir)
		raw = append(raw, File{Name: strings.TrimSuffix(name, ".zst"), Data: b})
		kz = append(kz, File{Name: name, Data: z})
	}
	if o.ZFSUtilsPkg != "" {
		udir := filepath.Join(o.Work, "zfs-utils")
		_ = os.RemoveAll(udir)
		if err := extract(o.ZFSUtilsPkg, udir, "usr/bin/zfs", "usr/bin/zpool", "usr/lib/lib*.so.*"); err == nil {
			_ = filepath.Walk(udir, func(p string, fi os.FileInfo, err error) error {
				if err == nil && fi.Mode().IsRegular() {
					b, _ := os.ReadFile(p)
					f := File{Name: strings.TrimPrefix(p, udir), Data: b}
					raw, kz = append(raw, f), append(kz, f)
				}
				return nil
			})
		}
	}
	for _, p := range initramfsHostFiles {
		if b, err := os.ReadFile(p); err == nil {
			f := File{Name: p, Data: b}
			raw, kz = append(raw, f), append(kz, f)
		}
	}
	for _, v := range []struct {
		path  *string
		name  string
		files []File
	}{{&in.InitramfsRaw, "initramfs-modules-raw.cpio", raw}, {&in.InitramfsKoZst, "initramfs-modules-zst.cpio", kz}} {
		var b bytes.Buffer
		if err := WriteNewc(&b, v.files); err != nil {
			return nil, err
		}
		*v.path = filepath.Join(dir, v.name)
		if err := os.WriteFile(*v.path, capBytes(b.Bytes(), o.StreamCap), 0o600); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// StreamSettings are the codecs for the kernel image and the initramfs. zstd stops at 19
// for the initramfs: higher levels use windows the kernel's streaming initramfs
// decompressor would have to allocate (scripts/Makefile.lib, cmd_zstd22).
var StreamSettings = []Setting{{"zstd", 1}, {"zstd", 3}, {"zstd", 9}, {"zstd", 19}, {"xz", 6}, {"lz4", 9}, {"gzip", 9}}

// ModuleSettings are the CONFIG_MODULE_COMPRESS_* candidates.
var ModuleSettings = []Setting{{"zstd", 3}, {"zstd", 9}, {"zstd", 19}, {"xz", 6}, {"gzip", 9}}

// RunKernel measures the kernel layer.
func RunKernel(o Options, in *KernelInputs, r *Report) error {
	streams := []struct {
		layer, shape, path string
		extra              []Setting
	}{
		{"kernel", "vmlinux " + in.Release, in.Vmlinux, []Setting{{"zstd", 22}}},
		{"initramfs", "modules-uncompressed", in.InitramfsRaw, nil},
		{"initramfs", "modules-ko.zst", in.InitramfsKoZst, nil},
	}
	for _, s := range streams {
		for _, st := range append(append([]Setting{}, StreamSettings...), s.extra...) {
			o.progress("%s %s %s", s.layer, s.shape, st.Name())
			res, err := StreamRun(o.Tools, st, s.path, o.Work, o.Reps)
			if err != nil {
				r.Skip(s.layer+" "+st.Name(), err.Error())
				continue
			}
			r.Rows = append(r.Rows, streamRow(s.layer, s.shape, st, res))
		}
	}
	// Modules: each compressed on its own, as Kbuild does.
	var orig int64
	for _, m := range in.Modules {
		orig += int64(len(m.Data))
	}
	shape := fmt.Sprintf("%d modules", len(in.Modules))
	r.Rows = append(r.Rows, Row{Layer: "modules", Shape: shape, Setting: "as-built", Method: "package",
		Files: len(in.Modules), OrigBytes: orig, CompBytes: in.ModulesAsBuilt, Ratio: float64(orig) / float64(in.ModulesAsBuilt),
		Notes: "the .ko.zst files exactly as the linux-runink package ships them"})
	var blocks [][]byte
	var concat bytes.Buffer
	for _, m := range in.Modules {
		blocks = append(blocks, m.Data)
		concat.Write(m.Data)
	}
	cpath := filepath.Join(o.Work, "modules.concat")
	if err := os.WriteFile(cpath, concat.Bytes(), 0o600); err != nil {
		return err
	}
	defer os.Remove(cpath)
	for _, st := range ModuleSettings {
		o.progress("modules %s", st.Name())
		sizes, err := BlockSizes(o.Tools, st, blocks, o.Work)
		if err != nil {
			r.Skip("modules "+st.Name(), err.Error())
			continue
		}
		var comp int64
		for _, s := range sizes {
			comp += int64(s)
		}
		res, err := StreamRun(o.Tools, st, cpath, o.Work, o.Reps)
		if err != nil {
			r.Skip("modules speed "+st.Name(), err.Error())
			continue
		}
		r.Rows = append(r.Rows, Row{Layer: "modules", Shape: shape, Setting: st.Name(), Method: "per-file+stream",
			Files: len(in.Modules), OrigBytes: orig, CompBytes: comp, Ratio: float64(orig) / float64(comp),
			CompressS: res.CompressWall.Seconds(), DecompressS: res.DecompressWall.Seconds(),
			DecompressBps: float64(orig) / res.DecompressWall.Seconds(),
			Notes:         "sizes: each module compressed alone; times: the sample as one stream"})
	}
	return nil
}

func streamRow(layer, shape string, st Setting, res Stream) Row {
	return Row{Layer: layer, Shape: shape, Setting: st.Name(), Method: "stream",
		OrigBytes: res.OrigBytes, CompBytes: res.CompBytes, Ratio: float64(res.OrigBytes) / float64(res.CompBytes),
		CompressS: res.CompressWall.Seconds(), DecompressS: res.DecompressWall.Seconds(),
		CompressBps:   float64(res.OrigBytes) / res.CompressWall.Seconds(),
		DecompressBps: float64(res.OrigBytes) / res.DecompressWall.Seconds(),
		CompressCPU:   [2]float64{res.CompressCPU[0].Seconds(), res.CompressCPU[1].Seconds()},
		DecompressCPU: [2]float64{res.DecompressCPU[0].Seconds(), res.DecompressCPU[1].Seconds()}}
}
