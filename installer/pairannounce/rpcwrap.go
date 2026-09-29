// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/org-runink/river/installer/internal/pair"
)

// rpcWrapper is the pairing sshd's ForceCommand. It runs as the pairing user, takes the
// operator's command from SSH_ORIGINAL_COMMAND, and either execs the receiving side of an
// rsync into the user's own staging dir, or forwards the command (and stdin) to the
// session over the RPC socket, whose owner runs it and streams the output back.
func rpcWrapper() int {
	cmd := os.Getenv("SSH_ORIGINAL_COMMAND")
	rpc, err := pair.ParseRPC(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "river-pair-announce:", err)
		return 126
	}
	if rpc.Verb == "rsync" {
		if err := os.Chdir(homeDir); err != nil {
			fmt.Fprintln(os.Stderr, "river-pair-announce:", err)
			return 1
		}
		_ = os.MkdirAll(filepath.Join("payload", rpc.Name), 0o700)
		env := []string{"PATH=/usr/bin:/bin", "HOME=" + homeDir, "LC_ALL=C"}
		err := syscall.Exec("/usr/bin/rsync", rpc.Argv, env) // #nosec G204 -- fixed program; argv validated by pair.ParseRPC (receiving side only, fixed destination)
		fmt.Fprintln(os.Stderr, "river-pair-announce: rsync:", err)
		return 1
	}
	c, err := net.Dial("unix", rpcSock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "river-pair-announce: the session is gone:", err)
		return 1
	}
	defer c.Close()
	if err := writeJSON(c, map[string]string{"cmd": cmd}); err != nil {
		return 1
	}
	go func() {
		_, _ = io.Copy(c, os.Stdin)
		_ = c.(*net.UnixConn).CloseWrite()
	}()
	for {
		typ, data, err := pair.ReadFrame(c)
		if err != nil {
			fmt.Fprintln(os.Stderr, "river-pair-announce: the session closed the connection")
			return 1
		}
		switch typ {
		case pair.FrameOut:
			_, _ = os.Stdout.Write(data)
		case pair.FrameExit:
			n, err := strconv.Atoi(string(data))
			if err != nil {
				return 1
			}
			return n
		}
	}
}
