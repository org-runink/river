// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

//go:build !(linux && amd64)

package pair

import (
	"errors"
	"os"
)

// MemFile is only implemented on linux/amd64, the only platform Runink River ships for.
func MemFile(name string, data []byte) (*os.File, error) {
	return nil, errors.New("memfd: not supported on this platform")
}
