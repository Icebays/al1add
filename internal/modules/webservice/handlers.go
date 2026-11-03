package webservice

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/vars"
)

func (m *Module) buildSubHandler(rule Rule, sub *SubRule) http.Handler {
	switch sub.Type {
	case "redirect", "redirect301":
		code := http.StatusFound
		if sub.Type == "redirect301" {
			code = http.StatusMovedPermanently
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := expandRedirect(sub.Backend, r)
			http.Redirect(w, r, target, code)
		})
	case "static":
		return m.staticHandler(sub)
	default:
		return m.proxyHandler(sub)
	}
}

func expandRedirect(backend string, r *http.Request) string {
	host, port, _ := net.SplitHostPort(r.Host)
	if host == "" {
		host = r.Host
	}
	repl := map[string]string{"host": host, "port": port, "uri": r.URL.RequestURI(), "path": r.URL.Path}
	out := backend
	for k, v := range repl {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return vars.Expand(out, nil)
}

func (m *Module) proxyHandler(sub *SubRule) http.Handler {
	target, err := url.Parse(vars.Expand(sub.Backend, nil))
	if err != nil || target.Scheme == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad backend URL: "+sub.Backend, http.StatusBadGateway)
		})
	}
	rp := &httputil.ReverseProxy{}
	keepHost := sub.HostHeaderOrigin
	keepIP, appendIP, cors := sub.KeepClientIP, sub.AppendClientIP, sub.CORS
	rp.Rewrite = func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		if keepHost {
			pr.Out.Host = pr.In.Host
		} else {
			pr.Out.Host = target.Host
		}
		clientIP := core.HostIP(pr.In.RemoteAddr).String()
		if keepIP {
			if appendIP {
				prior := pr.In.Header.Get("X-Forwarded-For")
				if prior != "" {
					clientIP = prior + ", " + clientIP
				}
			}
			pr.Out.Header.Set("X-Forwarded-For", clientIP)
		} else {
			pr.Out.Header.Del("X-Forwarded-For")
		}
		pr.Out.Header.Set("X-Real-IP", core.HostIP(pr.In.RemoteAddr).String())
		scheme := "http"
		if pr.In.TLS != nil {
			scheme = "https"
		}
		pr.Out.Header.Set("X-Forwarded-Proto", scheme)
		pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
	}
	if cors {
		inner := rp.ModifyResponse
		rp.ModifyResponse = func(resp *http.Response) error {
			if inner != nil {
				if err := inner(resp); err != nil {
					return err
				}
			}
			resp.Header.Set("Access-Control-Allow-Origin", "*")
			resp.Header.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS,PATCH")
			resp.Header.Set("Access-Control-Allow-Headers", "*")
			return nil
		}
	}
	if sub.IgnoreBackendTLS {
		rp.Transport = &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			ResponseHeaderTimeout: 60 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	name := sub.ID
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		m.log.Warnf("proxy %s: %v", name, err)
		http.Error(w, "backend error", http.StatusBadGateway)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cors && r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS,PATCH")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

func (m *Module) staticHandler(sub *SubRule) http.Handler {
	dir := sub.Backend
	if strings.HasPrefix(dir, "storage:") && m.app.Storage != nil {
		if root, _, err := m.app.Storage.Root(strings.TrimPrefix(dir, "storage:")); err == nil {
			dir = root
		}
	}
	fs := http.FileServer(http.Dir(dir))
	// strip the matched path prefix
	prefix := ""
	if _, p, ok := strings.Cut(sub.FrontDomain, "/"); ok {
		prefix = "/" + strings.Trim(p, "/")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if prefix != "" {
			http.StripPrefix(prefix, fs).ServeHTTP(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- auth

const wsCookie = "al1add_ws"

func (m *Module) authorize(w http.ResponseWriter, r *http.Request, sub *SubRule) bool {
	ip := core.RequestIP(r).String()
	// BasicAuth (local credentials on the sub-rule).
	if sub.BasicUser != "" && !sub.WebLogin {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(sub.BasicUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(sub.BasicPass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="al1add"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	if !sub.WebLogin {
		return true // IP-filter/group handled already
	}
	// Web login backed by a security group.
	if m.app.Groups == nil || sub.GroupID == "" {
		http.Error(w, "login not configured", http.StatusInternalServerError)
		return false
	}
	if m.app.Groups.IsAuthorized(sub.GroupID, ip) {
		return true
	}
	if c, err := r.Cookie(wsCookie); err == nil && m.validLoginCookie(sub, ip, c.Value) {
		m.app.Groups.Grant(sub.GroupID, ip, "web")
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/__al1add_login" {
		m.handleLogin(w, r, sub, ip)
		return false
	}
	m.loginPage(w, "")
	return false
}

func (m *Module) handleLogin(w http.ResponseWriter, r *http.Request, sub *SubRule, ip string) {
	_ = r.ParseForm()
	user, pass := r.FormValue("user"), r.FormValue("pass")
	if !m.app.Groups.Authenticate(sub.GroupID, user, pass) {
		m.log.Warnf("[%s] web login failed for sub-rule %s", ip, sub.ID)
		m.loginPage(w, "用戶名或密碼錯誤")
		return
	}
	m.app.Groups.Grant(sub.GroupID, ip, user)
	http.SetCookie(w, &http.Cookie{
		Name: wsCookie, Value: m.loginCookie(sub, ip, user), Path: "/",
		HttpOnly: true, MaxAge: 86400, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (m *Module) loginSecret(sub *SubRule) []byte {
	h := sha256.Sum256([]byte(sub.ID + "|" + sub.GroupID + "|al1add-ws-login"))
	return h[:]
}

func (m *Module) loginCookie(sub *SubRule, ip, user string) string {
	exp := strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10)
	payload := base64.RawURLEncoding.EncodeToString([]byte(user)) + "." + ip + "." + exp
	mac := hmac.New(sha256.New, m.loginSecret(sub))
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func (m *Module) validLoginCookie(sub *SubRule, ip, token string) bool {
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		return false
	}
	payload, sig := token[:i], token[i+1:]
	mac := hmac.New(sha256.New, m.loginSecret(sub))
	mac.Write([]byte(payload))
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return false
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 3 || parts[1] != ip {
		return false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	return err == nil && time.Now().Unix() <= exp
}

func (m *Module) loginPage(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	errHTML := ""
	if errMsg != "" {
		errHTML = fmt.Sprintf(`<p class="err">%s</p>`, errMsg)
	}
	fmt.Fprintf(w, loginHTML, errHTML)
}

const loginHTML = `<!doctype html><html lang="zh-Hant"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>登入</title><style>
body{font-family:system-ui,-apple-system,"Microsoft JhengHei",sans-serif;background:#0f1115;color:#e6e6e6;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
form{background:#1b1f27;padding:32px;border-radius:14px;width:300px;box-shadow:0 10px 40px rgba(0,0,0,.4)}
h1{font-size:20px;margin:0 0 20px;text-align:center}
input{width:100%%;box-sizing:border-box;margin:8px 0;padding:11px;border:1px solid #333a47;border-radius:8px;background:#11141a;color:#fff}
button{width:100%%;margin-top:14px;padding:11px;border:0;border-radius:8px;background:#3b82f6;color:#fff;font-size:15px;cursor:pointer}
.err{color:#f87171;font-size:13px;text-align:center}
</style></head><body>
<form method="post" action="/__al1add_login">
<h1>🔒 需要登入</h1>%s
<input name="user" placeholder="用戶名" autofocus required>
<input name="pass" type="password" placeholder="密碼" required>
<button type="submit">登入</button>
</form></body></html>`
