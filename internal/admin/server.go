// Package admin serves the management panel and its JSON API.
package admin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/vars"
)

//go:embed static
var staticFS embed.FS

// BaseConfig is the panel configuration (stored as base.alcf).
type BaseConfig struct {
	AdminUser     string `json:"AdminUser"`
	AdminPassHash string `json:"AdminPassHash"`
	ListenIP      string `json:"ListenIP"`
	Port          int    `json:"Port"`
	HTTPSPort     int    `json:"HTTPSPort"`
	SafeEntrance  string `json:"SafeEntrance"`
	LANOnly       bool   `json:"LANOnly"`
	AdminIPFilter string `json:"AdminIPFilter"`
	SessionHours  int    `json:"SessionHours"`
	SessionSecret string `json:"SessionSecret"`
	Title         string `json:"Title"`
}

// Server is the admin panel.
type Server struct {
	app  *core.App
	log  *logx.Logger
	mu   sync.RWMutex
	cfg  BaseConfig
	srvs []*http.Server

	failMu sync.Mutex
	fails  map[string]*failRec

	passphrase string
}

type failRec struct {
	n     int
	until time.Time
}

// New loads base config. initialPass is printed when a new admin is created.
func New(app *core.App, passphrase string) (*Server, string, error) {
	s := &Server{app: app, log: app.Logs.L("admin"), fails: map[string]*failRec{}, passphrase: passphrase}
	found, err := app.Store.Load("base", &s.cfg)
	if err != nil {
		return nil, "", fmt.Errorf("load base config: %w", err)
	}
	initial := ""
	if !found || s.cfg.AdminPassHash == "" {
		initial = randString(12)
		h, _ := bcrypt.GenerateFromPassword([]byte(initial), bcrypt.DefaultCost)
		if s.cfg.AdminUser == "" {
			s.cfg.AdminUser = "admin"
		}
		s.cfg.AdminPassHash = string(h)
	}
	s.normalize()
	if err := app.Store.Save("base", &s.cfg); err != nil {
		return nil, "", err
	}
	return s, initial, nil
}

func (s *Server) normalize() {
	c := &s.cfg
	if c.Port <= 0 {
		c.Port = 18890
	}
	if c.SessionHours <= 0 {
		c.SessionHours = 72
	}
	if c.SessionSecret == "" {
		c.SessionSecret = randString(32)
	}
	c.SafeEntrance = "/" + strings.Trim(strings.TrimSpace(c.SafeEntrance), "/")
	if c.Title == "" {
		c.Title = "Al1add"
	}
}

// ResetPassword sets user/password from the command line.
func (s *Server) ResetPassword(user, pass string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if user != "" {
		s.cfg.AdminUser = user
	}
	s.cfg.AdminPassHash = string(h)
	s.cfg.SessionSecret = randString(32)
	cfg := s.cfg
	s.mu.Unlock()
	return s.app.Store.Save("base", &cfg)
}

// SetPort changes the panel HTTP port (used by the CLI installer).
func (s *Server) SetPort(port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid port %d", port)
	}
	s.mu.Lock()
	s.cfg.Port = port
	cfg := s.cfg
	s.mu.Unlock()
	return s.app.Store.Save("base", &cfg)
}

// Port returns the configured panel HTTP port.
func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Port
}

// Start opens the panel listeners.
func (s *Server) Start() error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	h := s.handler()
	plain := &http.Server{Addr: net.JoinHostPort(cfg.ListenIP, strconv.Itoa(cfg.Port)), Handler: h, ReadHeaderTimeout: 15 * time.Second}
	ln, err := net.Listen("tcp", plain.Addr)
	if err != nil {
		return err
	}
	s.srvs = []*http.Server{plain}
	go func() { _ = plain.Serve(ln) }()
	s.log.Infof("panel listening on http://%s%s", displayAddr(cfg.ListenIP, cfg.Port), cfg.SafeEntrance)
	if cfg.HTTPSPort > 0 && s.app.Certs != nil {
		secure := &http.Server{
			Addr:              net.JoinHostPort(cfg.ListenIP, strconv.Itoa(cfg.HTTPSPort)),
			Handler:           h,
			ReadHeaderTimeout: 15 * time.Second,
			TLSConfig:         &tls.Config{GetCertificate: s.app.Certs.GetCertificate, MinVersion: tls.VersionTLS12},
		}
		tln, err := net.Listen("tcp", secure.Addr)
		if err != nil {
			s.log.Errorf("https listen: %v", err)
		} else {
			s.srvs = append(s.srvs, secure)
			go func() { _ = secure.ServeTLS(tln, "", "") }()
			s.log.Infof("panel listening on https://%s%s", displayAddr(cfg.ListenIP, cfg.HTTPSPort), cfg.SafeEntrance)
		}
	}
	return nil
}

// Stop closes the listeners.
func (s *Server) Stop() {
	for _, srv := range s.srvs {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	s.srvs = nil
}

func displayAddr(ip string, port int) string {
	if ip == "" {
		ip = "0.0.0.0"
	}
	return net.JoinHostPort(ip, strconv.Itoa(port))
}

// ---------------------------------------------------------------- routing

func (s *Server) handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/login", s.handleLogin)
	api.HandleFunc("POST /api/logout", s.handleLogout)
	api.HandleFunc("GET /api/session", s.auth(s.handleSession))
	api.HandleFunc("GET /api/info", s.auth(s.handleInfo))
	api.HandleFunc("GET /api/base", s.auth(s.handleGetBase))
	api.HandleFunc("PUT /api/base", s.auth(s.handlePutBase))
	api.HandleFunc("POST /api/base/password", s.auth(s.handlePassword))
	api.HandleFunc("GET /api/modules", s.auth(s.handleModules))
	api.HandleFunc("GET /api/m/{name}/config", s.auth(s.handleGetConfig))
	api.HandleFunc("PUT /api/m/{name}/config", s.auth(s.handlePutConfig))
	api.HandleFunc("GET /api/m/{name}/status", s.auth(s.handleStatus))
	api.HandleFunc("POST /api/m/{name}/action/{action}", s.auth(s.handleAction))
	api.HandleFunc("GET /api/logs", s.auth(s.handleLogs))
	api.HandleFunc("GET /api/vars", s.auth(s.handleVars))
	api.HandleFunc("GET /api/backup", s.auth(s.handleBackup))
	api.HandleFunc("POST /api/restore", s.auth(s.handleRestore))
	api.HandleFunc("/api/x/", s.auth(s.handleExtra))

	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))

	inner := http.NewServeMux()
	inner.Handle("/api/", api)
	inner.Handle("/", noCache(files))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		cfg := s.cfg
		s.mu.RUnlock()
		ip := core.RequestIP(r)
		if cfg.LANOnly && !core.IsLAN(ip) {
			http.Error(w, "forbidden: panel is restricted to LAN (disable 僅限區域網 / LAN-only in settings)", http.StatusForbidden)
			return
		}
		if cfg.AdminIPFilter != "" && s.app.IPFilter != nil && !s.app.IPFilter.Allow(cfg.AdminIPFilter, ip) {
			http.Error(w, "forbidden: blocked by the panel IP filter", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		prefix := cfg.SafeEntrance
		if prefix == "/" {
			inner.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == prefix {
			http.Redirect(w, r, prefix+"/", http.StatusFound)
			return
		}
		if !strings.HasPrefix(r.URL.Path, prefix+"/") {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		r2.URL.RawPath = ""
		inner.ServeHTTP(w, r2)
	})
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- sessions

const cookieName = "al1add_session"

func (s *Server) sign(payload string) string {
	s.mu.RLock()
	secret := s.cfg.SessionSecret
	s.mu.RUnlock()
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) newToken(user string) (string, time.Time) {
	s.mu.RLock()
	hours := s.cfg.SessionHours
	s.mu.RUnlock()
	exp := time.Now().Add(time.Duration(hours) * time.Hour)
	payload := base64.RawURLEncoding.EncodeToString([]byte(user)) + "." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + s.sign(payload), exp
}

func (s *Server) checkToken(tok string) (string, bool) {
	i := strings.LastIndexByte(tok, '.')
	if i < 0 {
		return "", false
	}
	payload, sig := tok[:i], tok[i+1:]
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return "", false
	}
	u, e, ok := strings.Cut(payload, ".")
	if !ok {
		return "", false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	user, err := base64.RawURLEncoding.DecodeString(u)
	if err != nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return string(user), string(user) == s.cfg.AdminUser
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, errors.New("not logged in"))
			return
		}
		if _, ok := s.checkToken(c.Value); !ok {
			writeErr(w, http.StatusUnauthorized, errors.New("session expired"))
			return
		}
		// CSRF: state-changing requests must come from our own page.
		if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "al1add" {
			writeErr(w, http.StatusForbidden, errors.New("missing request header"))
			return
		}
		h(w, r)
	}
}

func (s *Server) blocked(ip string) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	f := s.fails[ip]
	return f != nil && time.Now().Before(f.until)
}

func (s *Server) recordFail(ip string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	f := s.fails[ip]
	if f == nil {
		f = &failRec{}
		s.fails[ip] = f
	}
	f.n++
	if f.n >= 5 {
		f.until = time.Now().Add(10 * time.Minute)
		f.n = 0
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := core.RequestIP(r).String()
	if s.blocked(ip) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many failed attempts, try again later"))
		return
	}
	var req struct{ User, Pass string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.mu.RLock()
	user, hash := s.cfg.AdminUser, s.cfg.AdminPassHash
	s.mu.RUnlock()
	if req.User != user || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Pass)) != nil {
		s.recordFail(ip)
		s.log.Warnf("[%s] login failed for user %q", ip, req.User)
		writeErr(w, http.StatusUnauthorized, errors.New("wrong username or password"))
		return
	}
	tok, exp := s.newToken(user)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", Expires: exp, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	s.log.Infof("[%s] admin login successful", ip)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, map[string]any{"user": s.cfg.AdminUser, "title": s.cfg.Title})
}

// ---------------------------------------------------------------- handlers

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	title := s.cfg.Title
	s.mu.RUnlock()
	status := map[string]any{}
	for _, m := range s.app.Modules() {
		if st := s.app.Status(m.Name()); st != nil {
			status[m.Name()] = st
		}
	}
	writeJSON(w, map[string]any{
		"version": core.Version,
		"title":   title,
		"modules": s.app.Infos(),
		"vars":    vars.All(),
		"status":  status,
		"dataDir": s.app.DataDir,
	})
}

func (s *Server) handleGetBase(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	c := s.cfg
	s.mu.RUnlock()
	c.AdminPassHash = ""
	c.SessionSecret = ""
	writeJSON(w, c)
}

func (s *Server) handlePutBase(w http.ResponseWriter, r *http.Request) {
	var in BaseConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Port <= 0 || in.Port > 65535 || in.HTTPSPort < 0 || in.HTTPSPort > 65535 {
		writeErr(w, http.StatusBadRequest, errors.New("invalid port"))
		return
	}
	if strings.TrimSpace(in.AdminUser) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("admin user is required"))
		return
	}
	s.mu.Lock()
	old := s.cfg
	in.AdminPassHash = old.AdminPassHash
	in.SessionSecret = old.SessionSecret
	s.cfg = in
	s.normalize()
	cfg := s.cfg
	s.mu.Unlock()
	if err := s.app.Store.Save("base", &cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	restart := old.Port != cfg.Port || old.HTTPSPort != cfg.HTTPSPort || old.ListenIP != cfg.ListenIP
	writeJSON(w, map[string]any{"ok": true, "restart": restart, "entrance": cfg.SafeEntrance})
	if restart {
		go func() {
			time.Sleep(500 * time.Millisecond)
			s.Stop()
			if err := s.Start(); err != nil {
				s.log.Errorf("restart panel: %v — reverting to previous ports", err)
				s.mu.Lock()
				s.cfg.Port, s.cfg.HTTPSPort, s.cfg.ListenIP = old.Port, old.HTTPSPort, old.ListenIP
				c := s.cfg
				s.mu.Unlock()
				_ = s.app.Store.Save("base", &c)
				_ = s.Start()
			}
		}()
	}
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Old, New string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.mu.RLock()
	hash, user := s.cfg.AdminPassHash, s.cfg.AdminUser
	s.mu.RUnlock()
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Old)) != nil {
		writeErr(w, http.StatusBadRequest, errors.New("current password is wrong"))
		return
	}
	if len(req.New) < 6 {
		writeErr(w, http.StatusBadRequest, errors.New("new password must be at least 6 characters"))
		return
	}
	if err := s.ResetPassword(user, req.New); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	tok, exp := s.newToken(user)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", Expires: exp, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.app.Infos())
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	data, err := s.app.ConfigJSON(r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.SetConfig(r.PathValue("name"), data); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.app.Status(r.PathValue("name")))
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	res, err := s.app.Action(r.Context(), r.PathValue("name"), r.PathValue("action"), body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if raw, ok := res.(RawResponse); ok {
		raw.ServeHTTP(w, r)
		return
	}
	writeJSON(w, res)
}

// RawResponse lets an action stream its own response (downloads).
type RawResponse interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 300
	}
	writeJSON(w, s.app.Logs.Get(r.URL.Query().Get("module"), n))
}

func (s *Server) handleVars(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, vars.All())
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	plain := r.URL.Query().Get("plain") == "1"
	var buf bytes.Buffer
	if err := s.app.Store.ExportZip(&buf, plain); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	kind := "encrypted"
	if plain {
		kind = "plain"
	}
	name := fmt.Sprintf("al1add-backup-%s-%s.zip", kind, time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pass := r.FormValue("passphrase")
	if pass == "" {
		pass = s.passphrase
	}
	names, err := s.app.Store.ImportZip(bytes.NewReader(data), int64(len(data)), pass)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Infof("restored backup %s: %s", hdr.Filename, strings.Join(names, ", "))
	// Reload base config if it was part of the backup.
	var nb BaseConfig
	if found, err := s.app.Store.Load("base", &nb); err == nil && found && nb.AdminPassHash != "" {
		s.mu.Lock()
		s.cfg = nb
		s.normalize()
		s.mu.Unlock()
	}
	go s.app.ReloadAll()
	writeJSON(w, map[string]any{"ok": true, "restored": names})
}

func (s *Server) handleExtra(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/x/")
	name, _, _ := strings.Cut(path, "/")
	h := s.app.AdminHandler(name)
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h.ServeHTTP(w, r)
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func randString(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
