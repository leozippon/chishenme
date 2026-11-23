package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"chishenme/internal/store"
)

const (
	cookieName  = "chishenme_session"
	sessionTTL  = 30 * 24 * time.Hour
	failWindow  = 10 * time.Minute
	failLimit   = 5
	lockoutTime = 10 * time.Minute
)

type authed func(http.ResponseWriter, *http.Request, store.User)

func (s *Server) guard(h authed) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.session(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "请先登录")
			return
		}
		h(w, r, user)
	}
}

func (s *Server) session(r *http.Request) (store.User, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return store.User{}, false
	}
	id, version, ok := s.parseToken(cookie.Value, time.Now())
	if !ok {
		return store.User{}, false
	}
	user, err := s.store.User(id)
	if err != nil {
		if err != store.ErrNotFound {
			log.Printf("chishenme: read session user: %v", err)
		}
		return store.User{}, false
	}
	if user.TokenVersion() != version {
		return store.User{}, false
	}
	return user, true
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	now, from := time.Now(), clientIP(r)
	if !s.limit.allow(now) {
		log.Printf("chishenme: login throttled for %s from %s", logName(body.Username), from)
		writeErr(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}
	user, err := s.store.Authenticate(body.Username, body.Password)
	if err != nil {
		if err != store.ErrBadCredentials {
			s.fail(w, err)
			return
		}
		s.limit.record(now)
		log.Printf("chishenme: login failed for %s from %s", logName(body.Username), from)
		writeErr(w, http.StatusUnauthorized, "用户名或密码不正确")
		return
	}
	s.limit.clear()
	http.SetCookie(w, s.cookie(s.token(user, now), int(sessionTTL/time.Second), now.Add(sessionTTL)))
	log.Printf("chishenme: login ok for %s from %s", logName(user.Username), from)
	writeJSON(w, http.StatusOK, s.identity(user))
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, s.cookie("", -1, time.Time{}))
	writeOK(w)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, user store.User) {
	writeJSON(w, http.StatusOK, s.identity(user))
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.ChangePassword(user.ID, body.Current, body.Next); err != nil {
		s.fail(w, err)
		return
	}
	http.SetCookie(w, s.cookie("", -1, time.Time{}))
	writeOK(w)
}

func (s *Server) identity(user store.User) map[string]any {
	return map[string]any{
		"username": user.Username,
		"recipe":   s.cfg.DeepSeekKey != "",
	}
}

func (s *Server) cookie(value string, maxAge int, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) token(user store.User, now time.Time) string {
	payload := fmt.Sprintf("%d.%d.%d", user.ID, user.TokenVersion(), now.Add(sessionTTL).Unix())
	return payload + "." + s.sign(payload)
}

func (s *Server) sign(payload string) string {
	mac := hmac.New(sha256.New, s.cfg.Secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) parseToken(token string, now time.Time) (id int64, version int, ok bool) {
	cut := strings.LastIndexByte(token, '.')
	if cut < 0 {
		return 0, 0, false
	}
	payload, signature := token[:cut], token[cut+1:]
	if !hmac.Equal([]byte(signature), []byte(s.sign(payload))) {
		return 0, 0, false
	}
	fields := strings.Split(payload, ".")
	if len(fields) != 3 {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	version, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false
	}
	expiry, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || now.Unix() >= expiry {
		return 0, 0, false
	}
	return id, version, true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func logName(name string) string {
	if utf8.RuneCountInString(name) > 40 {
		name = string([]rune(name)[:40])
	}
	return strconv.Quote(name)
}

type throttle struct {
	mu     sync.Mutex
	fails  []time.Time
	locked time.Time
}

func newThrottle() *throttle { return &throttle{} }

func (t *throttle) allow(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !now.Before(t.locked)
}

func (t *throttle) record(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := now.Add(-failWindow)
	kept := t.fails[:0]
	for _, at := range t.fails {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	t.fails = kept
	if len(t.fails) >= failLimit {
		t.locked = now.Add(lockoutTime)
		t.fails = nil
	}
}

func (t *throttle) clear() {
	t.mu.Lock()
	t.fails = nil
	t.locked = time.Time{}
	t.mu.Unlock()
}
