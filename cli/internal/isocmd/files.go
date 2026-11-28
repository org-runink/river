// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package isocmd

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// copyOverlay copies the overlay tree src onto dst the way artools does (`cp -LR src/* dst`):
// symbolic links in src are followed and their targets copied, existing files in dst are
// overwritten in place (keeping their mode), new files and directories take src's mode. A
// missing src copies nothing.
func copyOverlay(src, dst string) error {
	st, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return copyDir(src, dst, 0)
}

func copyDir(src, dst string, depth int) error {
	if depth > 64 {
		return fmt.Errorf("%s: directory nesting too deep (a symlink loop?)", src)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		st, err := os.Stat(s) // follows a symlink, like cp -L
		if err != nil {
			return err
		}
		switch {
		case st.IsDir():
			if err := os.MkdirAll(d, st.Mode().Perm()); err != nil {
				return err
			}
			if err := copyDir(s, d, depth+1); err != nil {
				return err
			}
		case st.Mode().IsRegular():
			if err := copyFile(s, d, st.Mode().Perm()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: not a regular file or directory", s)
		}
	}
	return nil
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// cleanChroot removes what a built layer must not carry, as artools' clean_up_chroot does:
// initramfs images (the live one is built separately, an installed machine builds its own),
// pacman's sync databases and package cache, logs, temporary files, leftover .pacnew/.pacsave/
// .pacorig files, a GRUB menu and the machine id. Only regular files go from the log and
// cache directories, so the directories and an overlay's whiteouts stay.
func cleanChroot(root string) error {
	rmFiles := func(dir string, match func(string) bool) error {
		return filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.Type().IsRegular() && match(d.Name()) {
				return os.Remove(p)
			}
			return nil
		})
	}
	all := func(string) bool { return true }
	for _, dir := range []string{"var/lib/pacman/sync", "var/cache/pacman/pkg", "var/log"} {
		if err := rmFiles(dir, all); err != nil {
			return err
		}
	}
	if err := rmFiles("boot", func(n string) bool { return strings.HasPrefix(n, "initramfs") && strings.HasSuffix(n, ".img") }); err != nil {
		return err
	}
	for _, dir := range []string{"tmp", "var/tmp"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(root, dir, e.Name())); err != nil {
				return err
			}
		}
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch strings.TrimPrefix(p, root) {
			case "/proc", "/sys", "/dev", "/run":
				return filepath.SkipDir
			}
			return nil
		}
		n := d.Name()
		if d.Type().IsRegular() && (strings.HasSuffix(n, ".pacnew") || strings.HasSuffix(n, ".pacsave") || strings.HasSuffix(n, ".pacorig")) {
			return os.Remove(p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, f := range []string{"boot/grub/grub.cfg", "etc/machine-id"} {
		if err := os.Remove(filepath.Join(root, f)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// injectRunink puts the [runink] repository (the local file:// repository this build made)
// in front of the first repository section of a pacman.conf, so its linux-runink, runink-zfs
// and rebuilt packages win over any same-named package further down.
func injectRunink(conf, localRepo string) (string, error) {
	if strings.Contains(conf, "\n[runink]") || strings.HasPrefix(conf, "[runink]") {
		return "", fmt.Errorf("the pacman configuration already has a [runink] section")
	}
	// The image installs nothing from [blackarch]: it is configured on installed machines for
	// their owner (pacman/pacman.conf.in), never a build source.
	for _, l := range strings.Split(conf, "\n") {
		if strings.TrimSpace(l) == "[blackarch]" {
			return "", fmt.Errorf("the build's pacman configuration has a [blackarch] section; the image never installs from it")
		}
	}
	block := "[runink]\nSigLevel = Optional TrustAll\nServer = file://" + localRepo + "\n\n"
	lines := strings.SplitAfter(conf, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") && t != "[options]" {
			return strings.Join(lines[:i], "") + block + strings.Join(lines[i:], ""), nil
		}
	}
	return "", fmt.Errorf("the pacman configuration has no repository section")
}
