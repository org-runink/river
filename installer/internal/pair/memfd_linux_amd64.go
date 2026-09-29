// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package pair

import (
	"os"
	"syscall"
	"unsafe"
)

const sysMemfdCreate = 319 // memfd_create(2) on x86-64; the syscall package does not name it

// MemFile returns an anonymous in-memory file holding data (memfd_create(2)): it has no
// name in any filesystem, never touches a disk, and disappears with its last descriptor.
// A child process reads it as /dev/fd/N, and every open of that path starts at offset 0,
// so a script may read it more than once.
func MemFile(name string, data []byte) (*os.File, error) {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return nil, err
	}
	const mfdCloexec = 1
	fd, _, e := syscall.Syscall(sysMemfdCreate, uintptr(unsafe.Pointer(p)), mfdCloexec, 0) // #nosec G103 -- memfd_create(2) needs a C string pointer; the syscall package has no wrapper
	if e != 0 {
		return nil, e
	}
	f := os.NewFile(fd, "memfd:"+name)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
