// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Command river-pair-announce is the TARGET side of opt-in LAN install pairing
// (docs/INSTALL.md, "LAN installs"). It runs only on the Runink River live installer, and
// only when the operator sitting at this machine chose "Let another machine install this
// one" in runink-install (or ran it from the second console):
//
//	river-pair-announce [--iface IF]   the local console: starts the session service, shows
//	                                   the pairing code and this session's host key
//	                                   fingerprint, asks "Allow? [y/N]" when an operator
//	                                   pairs, shows progress and the recovery key. Leaving it
//	                                   (N, Ctrl-C, end of install) tears everything down.
//	river-pair-announce serve --iface IF
//	                                   the session itself, as a one-shot s6 service under the
//	                                   live scan directory (never in the boot database): the
//	                                   link-local announcement, the pairing endpoint and,
//	                                   once the local operator allowed a pairing, a
//	                                   restricted sshd for that operator's key only.
//	river-pair-announce rpc            the pairing sshd's ForceCommand (the only thing an
//	                                   operator can run here); see internal/pair/rpc.go.
//
// Exit status: 0 success, 1 failure, 2 usage.
package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	mode := "console"
	if len(args) > 0 && (args[0] == "serve" || args[0] == "rpc" || args[0] == "console") {
		mode, args = args[0], args[1:]
	}
	var err error
	switch mode {
	case "serve":
		err = serve(args)
	case "rpc":
		os.Exit(rpcWrapper())
	default:
		err = console(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "river-pair-announce:", err)
		os.Exit(1)
	}
}
