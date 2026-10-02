// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/org-runink/river/installer/internal/qr"
)

//go:embed web
var webFS embed.FS

// Guard decides who may talk to the API. The installer binds loopback only; on top of that,
// every request must come from a socket owned by root or by one of AllowUIDs (the kiosk
// user, or the live desktop user whose browser shows the installer), and carry the right
// Host header (no DNS rebinding) and, for changes, a JSON body with the X-River header (no
// cross-site form posts from a page the desktop browser happens to show).
type Guard struct {
	Port       int
	AllowUIDs  []int
	AllowUsers []string // resolved at each request: the kiosk user may be created after start
	ProcRoot   string   // "/proc"; empty disables the peer check (tests, --demo)
}

func (g Guard) hostOK(host string) bool {
	for _, h := range []string{"[::1]", "localhost", "127.0.0.1"} {
		if host == h+":"+itoa(g.Port) {
			return true
		}
	}
	return false
}

func (g Guard) peerOK(r *http.Request) bool {
	if g.ProcRoot == "" {
		return true
	}
	la, ok1 := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	ra, err := net.ResolveTCPAddr("tcp", r.RemoteAddr)
	if !ok1 || err != nil {
		return false
	}
	uid, err := peerUID(g.ProcRoot, la, ra)
	if err != nil {
		return false
	}
	if uid == 0 {
		return true
	}
	for _, u := range g.AllowUIDs {
		if u == uid {
			return true
		}
	}
	for _, name := range g.AllowUsers {
		if s := userID(name); s != "" && s == itoa(uid) {
			return true
		}
	}
	return false
}

func (g Guard) wrap(next http.Handler, frameSrc string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		csp := "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'"
		if frameSrc != "" {
			csp += "; frame-src " + frameSrc
		}
		h.Set("Content-Security-Policy", csp)
		if !g.hostOK(r.Host) {
			http.Error(w, "bad host", http.StatusMisdirectedRequest)
			return
		}
		if !g.peerOK(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-River") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func itoa(n int) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---- JSON helpers ------------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 128*1024))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// reply turns an action's result into a response: the new state, or a translatable error.
func reply(w http.ResponseWriter, err error, view func() any) {
	var ae APIError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, view())
	case errors.As(err, &ae):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": ae.Key})
	case errors.Is(err, ErrState):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "err.state"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "err.internal"})
	}
}

func post(mux *http.ServeMux, path string, h func(w http.ResponseWriter, r *http.Request)) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	})
}

// staticHandler serves the embedded UI.
func staticHandler() http.Handler {
	sub, _ := fs.Sub(webFS, "web")
	return http.FileServer(http.FS(sub))
}

// events streams "state" and "log" events; the first event is always the current state.
func events(h *hub, current func() any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Connection", "keep-alive")
		c := h.subscribe()
		defer h.unsubscribe(c)
		b, _ := json.Marshal(current())
		_, _ = io.WriteString(w, "retry: 1000\nevent: state\ndata: "+string(b)+"\n\n")
		fl.Flush()
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case m := <-c:
				if _, err := w.Write(m); err != nil {
					return
				}
				fl.Flush()
			case <-tick.C:
				if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
					return
				}
				fl.Flush()
			}
		}
	}
}

// Handler is the install flow's HTTP API and UI.
func (wz *Wizard) Handler(g Guard) http.Handler {
	mux := http.NewServeMux()
	view := func() any { return wz.View() }
	mux.Handle("/", staticHandler())
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, wz.View()) })
	mux.HandleFunc("/api/events", events(wz.hub, view))
	// The running edition's own mark (descriptor "icon"): favicon and header. 404 without one.
	mux.HandleFunc("/edition-icon", func(w http.ResponseWriter, r *http.Request) {
		p := wz.self.Icon
		fi, err := os.Stat(p)
		if p == "" || err != nil || !fi.Mode().IsRegular() || fi.Size() > 512*1024 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", iconType(p))
		http.ServeFile(w, r, p)
	})
	mux.HandleFunc("/api/install/log", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"lines": wz.Log()})
	})
	post(mux, "/api/locale", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Lang, Keyboard string }
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, wz.SetLocale(in.Lang, in.Keyboard), view)
	})
	post(mux, "/api/welcome", func(w http.ResponseWriter, r *http.Request) { reply(w, wz.Welcome(), view) })
	post(mux, "/api/back", func(w http.ResponseWriter, r *http.Request) { reply(w, wz.Back(), view) })
	post(mux, "/api/edition", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Edition, Role string }
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, wz.ChooseEdition(in.Edition, in.Role), view)
	})
	post(mux, "/api/network/auto", func(w http.ResponseWriter, r *http.Request) { reply(w, wz.NetAuto(), view) })
	post(mux, "/api/network/scan", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		_, err := wz.WifiScan(ctx)
		reply(w, err, view)
	})
	post(mux, "/api/network/wifi", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ SSID, Passphrase string }
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, wz.WifiConnect(in.SSID, in.Passphrase), view)
	})
	post(mux, "/api/network/continue", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Offline bool }
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, wz.NetContinue(in.Offline), view)
	})
	post(mux, "/api/machine/probe", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Lab bool }
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, wz.Reprobe(in.Lab), view)
	})
	post(mux, "/api/machine/erase-dialog", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Open bool }
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, wz.EraseDialog(in.Open), view)
	})
	post(mux, "/api/machine/confirm", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Word  string   `json:"word"`
			Disks []string `json:"disks"`
		}
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, wz.ConfirmErase(in.Word, in.Disks), view)
	})
	post(mux, "/api/account", func(w http.ResponseWriter, r *http.Request) {
		var in AccountInput
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		fields, err := wz.SetAccount(in)
		if fields != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "err.form", "fields": fields})
			return
		}
		reply(w, err, view)
	})
	mux.HandleFunc("/api/recovery", func(w http.ResponseWriter, r *http.Request) {
		key, err := wz.RecoveryKey()
		if err != nil {
			reply(w, err, nil)
			return
		}
		c, err := qr.Encode([]byte(key))
		if err != nil {
			reply(w, err, nil)
			return
		}
		writeJSON(w, 200, map[string]any{"key": groupKey(key), "qr": c.Rows()})
	})
	mux.HandleFunc("/api/recovery/usb", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in struct{ Dev string }
			if readJSON(r, &in) != nil {
				reply(w, APIError{"err.request"}, nil)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
			defer cancel()
			reply(w, wz.SaveKeyToUSB(ctx, in.Dev), view)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		d, err := wz.USBDrives(ctx)
		if err != nil {
			reply(w, APIError{"err.usb.list"}, nil)
			return
		}
		if d == nil {
			d = []USBDrive{}
		}
		writeJSON(w, 200, map[string]any{"drives": d})
	})
	post(mux, "/api/recovery/ack", func(w http.ResponseWriter, r *http.Request) {
		// Groups of the key typed back, keyed by the 1-based group number that
		// Recovery.Confirm asked for. This replaced a {"written": true} tickbox, which
		// any client could send without the key ever having been read.
		var in struct {
			Groups map[string]string `json:"groups"`
		}
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, wz.AckRecovery(in.Groups), view)
	})
	post(mux, "/api/install/retry", func(w http.ResponseWriter, r *http.Request) { reply(w, wz.RetryInstall(), view) })
	post(mux, "/api/reboot", func(w http.ResponseWriter, r *http.Request) { reply(w, wz.Reboot(), view) })
	return g.wrap(mux, "")
}

// Handler is the first boot's HTTP API and UI. Setup pages are framed from their own loopback
// origin, so frame-src allows loopback (and nothing else).
func (f *Firstboot) Handler(g Guard) http.Handler {
	mux := http.NewServeMux()
	view := func() any { return f.View() }
	mux.Handle("/", staticHandler())
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, f.View()) })
	mux.HandleFunc("/api/events", events(f.hub, view))
	mux.HandleFunc("/api/firstboot/page", func(w http.ResponseWriter, r *http.Request) {
		u, err := f.PageURL(r.URL.Query().Get("name"))
		if err != nil {
			reply(w, err, nil)
			return
		}
		writeJSON(w, 200, map[string]string{"url": u})
	})
	post(mux, "/api/locale", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Lang, Keyboard string }
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, f.SetLang(in.Lang), view)
	})
	post(mux, "/api/firstboot/skip", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name string }
		if readJSON(r, &in) != nil {
			reply(w, APIError{"err.request"}, nil)
			return
		}
		reply(w, f.SkipPage(in.Name), view)
	})
	post(mux, "/api/firstboot/retry", func(w http.ResponseWriter, r *http.Request) { reply(w, f.Retry(), view) })
	post(mux, "/api/firstboot/finish", func(w http.ResponseWriter, r *http.Request) {
		reply(w, f.Finish(r.Context()), view)
	})
	return g.wrap(mux, "http://[::1]:* http://127.0.0.1:*")
}
