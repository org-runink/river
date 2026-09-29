// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Command river-fake-metadata serves a stand-in GCE metadata server (and the Secret Manager
// access call) for build/cloud-image-test.sh. It is a TEST tool and never ships on an image.
//
//	river-fake-metadata --config C --listen 127.0.0.1:8169   a normal HTTP listener
//	river-fake-metadata --config C --stdio                   one connection on stdin/stdout
//
// --stdio is what QEMU's user-mode network runs for each guest connection to
// 169.254.169.254:80 (-netdev user,guestfwd=tcp:169.254.169.254:80-cmd:...). Every request
// is appended to --log, so the test can show what the guest asked for.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/org-runink/river/installer/internal/cloud/fake"
)

func main() {
	cfgF := flag.String("config", "", "fake server state (JSON)")
	listen := flag.String("listen", "", "address to listen on")
	stdio := flag.Bool("stdio", false, "serve one connection on stdin/stdout")
	logF := flag.String("log", "", "append one line per request to this file")
	flag.Parse()
	cfg, err := fake.Load(*cfgF)
	if err != nil {
		fmt.Fprintln(os.Stderr, "river-fake-metadata:", err)
		os.Exit(1)
	}
	h := cfg.Handler()
	if *logF != "" {
		f, err := os.OpenFile(*logF, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "river-fake-metadata:", err)
			os.Exit(1)
		}
		lg := log.New(f, "", log.LstdFlags)
		inner := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lg.Printf("%s %s", r.Method, r.URL.Path)
			inner.ServeHTTP(w, r)
		})
	}
	switch {
	case *stdio:
		serveStdio(h)
	case *listen != "":
		srv := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
		log.Fatal(srv.ListenAndServe())
	default:
		fmt.Fprintln(os.Stderr, "river-fake-metadata: give --listen or --stdio")
		os.Exit(2)
	}
}

// stdioConn is stdin/stdout as a net.Conn.
type stdioConn struct {
	io.Reader
	io.Writer
}

func (stdioConn) Close() error                     { return nil }
func (stdioConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (stdioConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (stdioConn) SetDeadline(time.Time) error      { return nil }
func (stdioConn) SetReadDeadline(time.Time) error  { return nil }
func (stdioConn) SetWriteDeadline(time.Time) error { return nil }

// oneListener hands out one connection, then blocks until that connection is done.
type oneListener struct {
	once sync.Once
	c    net.Conn
	done chan struct{}
}

func (l *oneListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.c })
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, errors.New("done")
}
func (l *oneListener) Close() error   { return nil }
func (l *oneListener) Addr() net.Addr { return &net.TCPAddr{} }

func serveStdio(h http.Handler) {
	l := &oneListener{c: stdioConn{os.Stdin, os.Stdout}, done: make(chan struct{})}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second,
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				close(l.done)
			}
		}}
	_ = srv.Serve(l)
}
