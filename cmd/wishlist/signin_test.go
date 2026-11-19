// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

// The sign in, end to end through the real public handler: no provider, no
// browser. The credentials live in fakeBackend, so these run everywhere. The
// same flow against a real PostgreSQL is TestSignInEndToEndOnPostgreSQL.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
	"github.com/ostendorf-cloud/wishlist/internal/auth"
	"github.com/ostendorf-cloud/wishlist/internal/config"
	"github.com/ostendorf-cloud/wishlist/internal/dbtest"
	"github.com/ostendorf-cloud/wishlist/internal/store"
	"github.com/ostendorf-cloud/wishlist/internal/webapp"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

const (
	testLogin    = "alice@example.invalid"
	testPassword = "correct horse battery staple"
)

// clock is a settable time, so a test can reach the next TOTP step.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// browser is a cookie jar and a remote address in front of a handler.
type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]string
	addr    string
}

func newBrowser(t *testing.T, h http.Handler) *browser {
	t.Helper()
	return &browser{t: t, h: h, cookies: map[string]string{}, addr: "192.0.2.10:4000"}
}

func (b *browser) do(method, path string, body any, token string, header ...string) *httptest.ResponseRecorder {
	b.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(b.t.Context(), method, path, reader)
	req.RemoteAddr = b.addr
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	for name, value := range b.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(b.cookies, c.Name)
			continue
		}
		b.cookies[c.Name] = c.Value
	}
	return rec
}

func (b *browser) info() auth.Info {
	b.t.Helper()
	rec := b.do(http.MethodGet, auth.Prefix+"/session", nil, "")
	var info auth.Info
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &info) != nil {
		b.t.Fatalf("GET session = %d %s", rec.Code, rec.Body)
	}
	return info
}

// signup registers and returns the enrolment secret, or the refusal.
func (b *browser) signup(login string) (string, *httptest.ResponseRecorder) {
	b.t.Helper()
	rec := b.do(http.MethodPost, auth.Prefix+"/signup", map[string]string{"login": login, "password": testPassword}, b.token())
	var out account.SignupResult
	if rec.Code == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			b.t.Fatal(err)
		}
	}
	return out.Enrolment.Secret, rec
}

// token is the CSRF token of the session the browser holds, if any.
func (b *browser) token() string {
	if len(b.cookies) == 0 {
		return ""
	}
	rec := b.do(http.MethodGet, auth.Prefix+"/session", nil, "")
	var info auth.Info
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	return info.CSRFToken
}

func (b *browser) post(path, login, password, code string) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.do(http.MethodPost, auth.Prefix+path,
		map[string]string{"login": login, "password": password, "code": code}, b.token())
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := account.TOTPCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

type rig struct {
	h     http.Handler
	clock *clock
	owner *ownertest.Fake
}

// newRig builds the public handler the way main does, with the account store
// and the limits given by the caller.
func newRig(t *testing.T, backend accounts.Backend, open bool, tune func(*auth.Config)) rig {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	fake := ownertest.New()
	deps := handlerDeps{
		owners:   fake,
		accounts: accounts.New(backend),
		cfg:      config.Config{RegistrationOpen: open, AllowPlainHTTP: true},
		tune: func(cfg *auth.Config) {
			cfg.Now = c.Now
			// Tests share one remote address: lift the limit unless a test
			// is about the limit.
			cfg.ClientBurst, cfg.LoginBurst, cfg.CeilingBurst = 1000, 1000, 1000
			if tune != nil {
				tune(cfg)
			}
		},
	}
	h, err := buildHandler(slog.New(slog.DiscardHandler), deps)
	if err != nil {
		t.Fatal(err)
	}
	return rig{h: h, clock: c, owner: fake}
}

// register signs up and confirms, which starts the session.
func (r rig) register(t *testing.T, b *browser, login string) string {
	t.Helper()
	secret, rec := b.signup(login)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d %s", rec.Code, rec.Body)
	}
	if rec := b.post("/confirm", login, testPassword, codeAt(t, secret, r.clock.Now())); rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body)
	}
	return secret
}

// login uses the code of the next TOTP step: a code is good once.
func (r rig) login(t *testing.T, b *browser, login, secret string) *httptest.ResponseRecorder {
	t.Helper()
	r.clock.Add(30 * time.Second)
	return b.post("/login", login, testPassword, codeAt(t, secret, r.clock.Now()))
}

func ownerStatus(b *browser, method, path string, body any, token string) int {
	return b.do(method, owner.Prefix+path, body, token).Code
}

// probe runs the security rules against a handler and returns what it finds
// broken. A good wiring returns nothing. The mutation tests weaken one part
// and expect probe to notice, so the checks are proven to check.
func probe(t *testing.T, r rig, registrationClosed bool) []string {
	t.Helper()
	var found []string
	add := func(format string, args ...any) { found = append(found, fmt.Sprintf(format, args...)) }

	b := newBrowser(t, r.h)
	if registrationClosed {
		if _, rec := b.signup(testLogin); rec.Code != http.StatusForbidden {
			add("registration is closed but signup answered %d", rec.Code)
		}
		return found
	}

	if ownerStatus(b, http.MethodGet, "/lists/999", nil, "") != http.StatusUnauthorized {
		add("an owner request without a session was not refused with 401")
	}
	secret := r.register(t, b, testLogin)
	token := b.token()
	if token == "" {
		add("a signed in browser got no CSRF token")
	}
	if got := ownerStatus(b, http.MethodGet, "/lists", nil, ""); got != http.StatusOK {
		add("a signed in owner request answered %d, want 200", got)
	}
	list := map[string]any{"title": "Birthday"}
	if got := ownerStatus(b, http.MethodPost, "/lists", list, ""); got != http.StatusForbidden {
		add("an unsafe request without the CSRF token answered %d, want 403", got)
	}
	if got := ownerStatus(b, http.MethodPost, "/lists", list, "not-the-token"); got != http.StatusForbidden {
		add("an unsafe request with a wrong CSRF token answered %d, want 403", got)
	}
	if got := ownerStatus(b, http.MethodPost, "/lists", list, token); got != http.StatusCreated {
		add("an unsafe request with the CSRF token answered %d, want 201", got)
	}

	stolen := map[string]string{}
	for k, v := range b.cookies {
		stolen[k] = v
	}
	if rec := b.do(http.MethodPost, auth.Prefix+"/logout", nil, token); rec.Code != http.StatusNoContent {
		add("logout answered %d, want 204", rec.Code)
	}
	if got := ownerStatus(b, http.MethodGet, "/lists", nil, ""); got != http.StatusUnauthorized {
		add("the owner API still answered %d after logout", got)
	}
	thief := newBrowser(t, r.h)
	thief.cookies = stolen
	if got := ownerStatus(thief, http.MethodGet, "/lists", nil, ""); got != http.StatusUnauthorized {
		add("a copy of the session cookie still worked after logout (%d)", got)
	}
	if rec := r.login(t, b, testLogin, secret); rec.Code != http.StatusOK {
		add("login after logout answered %d, want 200", rec.Code)
	} else if got := ownerStatus(b, http.MethodGet, "/lists", nil, ""); got != http.StatusOK {
		add("the owner API answered %d after a new login", got)
	}
	forged := newBrowser(t, r.h)
	forged.cookies["session"] = "forged-session-id"
	if got := ownerStatus(forged, http.MethodGet, "/lists", nil, ""); got != http.StatusUnauthorized {
		add("a forged session cookie answered %d, want 401", got)
	}
	return found
}

func TestSignInEndToEndWithNoProvider(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	if found := probe(t, r, false); len(found) != 0 {
		t.Fatalf("the sign in is broken: %v", found)
	}
}

// The same flow against the real credentials tables and the real owner rows.
func TestSignInEndToEndOnPostgreSQL(t *testing.T) {
	core, err := store.Open(t.Context(), dbtest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(core.Close)
	if err := core.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	h, err := buildHandler(slog.New(slog.DiscardHandler), handlerDeps{
		owners:   core.Owners(),
		accounts: accounts.New(core.Accounts()),
		cfg:      config.Config{RegistrationOpen: true, AllowPlainHTTP: true},
		tune: func(cfg *auth.Config) {
			cfg.Now = c.Now
			cfg.ClientBurst, cfg.LoginBurst, cfg.CeilingBurst = 1000, 1000, 1000
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := rig{h: h, clock: c}
	if found := probe(t, r, false); len(found) != 0 {
		t.Fatalf("the sign in is broken on PostgreSQL: %v", found)
	}

	// The owner id of the API is the owners row of the account.
	b := newBrowser(t, h)
	r.register(t, b, "bob@example.invalid")
	token := b.token()
	rec := b.do(http.MethodPost, owner.Prefix+"/lists", map[string]any{"title": "Mine"}, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create list = %d %s", rec.Code, rec.Body)
	}
	other := newBrowser(t, h)
	r.register(t, other, "carol@example.invalid")
	rec = other.do(http.MethodGet, owner.Prefix+"/lists", nil, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Mine") {
		t.Errorf("a second owner sees %d %s", rec.Code, rec.Body)
	}
}

func TestACopyOfTheSessionCookieDiesWithTheLogout(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	b := newBrowser(t, r.h)
	r.register(t, b, testLogin)
	copyOf := map[string]string{}
	for k, v := range b.cookies {
		copyOf[k] = v
	}
	if len(copyOf) != 1 {
		t.Fatalf("cookies = %v, want the session cookie", copyOf)
	}
	b.do(http.MethodPost, auth.Prefix+"/logout", nil, b.token())
	thief := newBrowser(t, r.h)
	thief.cookies = copyOf
	if got := ownerStatus(thief, http.MethodGet, "/lists", nil, ""); got != http.StatusUnauthorized {
		t.Errorf("the stolen cookie answered %d after logout, want 401", got)
	}
	if thief.info().SignedIn {
		t.Error("the session endpoint still says signed in for the stolen cookie")
	}
}

func TestTheOwnerIDIsTheAccountRowAndNeverTheLogin(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	alice, bob := newBrowser(t, r.h), newBrowser(t, r.h)
	r.register(t, alice, testLogin)
	r.register(t, bob, "bob@example.invalid")

	if rec := alice.do(http.MethodPost, owner.Prefix+"/lists", map[string]any{"title": "Alice only"}, alice.token()); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if body := bob.do(http.MethodGet, owner.Prefix+"/lists", nil, "").Body.String(); strings.Contains(body, "Alice only") {
		t.Errorf("bob sees alice's list: %s", body)
	}
	// ownertest scopes by the owner id the resolver returned: alice is 1, bob 2.
	lists, err := r.owner.Lists(t.Context(), 1, true)
	if err != nil || len(lists) != 1 {
		t.Fatalf("owner 1 has %v, %v, want alice's list", lists, err)
	}
	if lists, _ := r.owner.Lists(t.Context(), 2, true); len(lists) != 0 {
		t.Errorf("owner 2 has %v, want none", lists)
	}
}

func TestUnauthenticatedOwnerRequestsAnswerOneThingAndRevealNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	b := newBrowser(t, r.h)
	// Create a list as somebody else, so one id exists and the other does not.
	other := newBrowser(t, r.h)
	r.register(t, other, "bob@example.invalid")
	other.do(http.MethodPost, owner.Prefix+"/lists", map[string]any{"title": "x"}, other.token())

	bodies := map[string]bool{}
	for _, path := range []string{"/lists", "/lists/1", "/lists/99999", "/wishes/1", "/lists/abc"} {
		rec := b.do(http.MethodGet, owner.Prefix+path, nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401", path, rec.Code)
		}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		bodies[env.Error.Code+"|"+env.Error.Message] = true
	}
	if len(bodies) != 1 {
		t.Errorf("unauthenticated answers differ by target: %v", bodies)
	}
}

func TestAFailedLoginIsOneAnswerWhateverWasWrong(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	b := newBrowser(t, r.h)
	secret := r.register(t, b, testLogin)
	b.do(http.MethodPost, auth.Prefix+"/logout", nil, b.token())
	r.clock.Add(30 * time.Second)
	good := codeAt(t, secret, r.clock.Now())

	answers := map[string]bool{}
	for name, try := range map[string][3]string{
		"unknown login":  {"nobody@example.invalid", testPassword, good},
		"wrong password": {testLogin, "not the password at all", good},
		"wrong code":     {testLogin, testPassword, "000000"},
	} {
		rec := b.post("/login", try[0], try[1], try[2])
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d %s, want 401", name, rec.Code, rec.Body)
		}
		var env struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		answers[env.Error.Code+"|"+env.Error.Message] = true
	}
	if len(answers) != 1 {
		t.Errorf("failed logins answer differently: %v", answers)
	}
}

func TestACodeIsGoodOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	b := newBrowser(t, r.h)
	secret := r.register(t, b, testLogin)
	b.do(http.MethodPost, auth.Prefix+"/logout", nil, b.token())
	r.clock.Add(30 * time.Second)
	code := codeAt(t, secret, r.clock.Now())
	if rec := b.post("/login", testLogin, testPassword, code); rec.Code != http.StatusOK {
		t.Fatalf("first use = %d %s", rec.Code, rec.Body)
	}
	b.do(http.MethodPost, auth.Prefix+"/logout", nil, b.token())
	if rec := b.post("/login", testLogin, testPassword, code); rec.Code != http.StatusUnauthorized {
		t.Errorf("a replayed code = %d, want 401", rec.Code)
	}
}

func TestRegistrationIsClosedBySetting(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), false, nil)
	b := newBrowser(t, r.h)
	_, rec := b.signup(testLogin)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("signup with registration closed = %d %s, want 403", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "signup is not available") {
		t.Errorf("the refusal is %s, want govue's documented message", rec.Body)
	}
	if b.info().RegistrationOpen {
		t.Error("the session endpoint says registration is open")
	}
	// Nothing was created: the same login cannot sign in.
	r.clock.Add(30 * time.Second)
	if rec := b.post("/login", testLogin, testPassword, "123456"); rec.Code != http.StatusUnauthorized {
		t.Errorf("login of the refused account = %d, want 401", rec.Code)
	}
	if found := probe(t, r, true); len(found) != 0 {
		t.Errorf("closed registration: %v", found)
	}

	open := newRig(t, newFakeBackend(), true, nil)
	if !newBrowser(t, open.h).info().RegistrationOpen {
		t.Error("registration is not open by default in the open rig")
	}
}

func TestLoginAndRegistrationAreLimitedPerAddress(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, func(c *auth.Config) { c.ClientBurst = 3 })
	b := newBrowser(t, r.h)
	for i := 1; i <= 3; i++ {
		if rec := b.post("/login", fmt.Sprintf("user%d@example.invalid", i), testPassword, "123456"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i, rec.Code)
		}
	}
	rec := b.post("/login", "user4@example.invalid", testPassword, "123456")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("attempt 4 from one address = %d %v, want 429 with Retry-After", rec.Code, rec.Header())
	}
	if _, rec := b.signup("new@example.invalid"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("signup from the same address = %d, want 429", rec.Code)
	}
	elsewhere := newBrowser(t, r.h)
	elsewhere.addr = "192.0.2.77:4000"
	if rec := elsewhere.post("/login", "user5@example.invalid", testPassword, "123456"); rec.Code != http.StatusUnauthorized {
		t.Errorf("another address = %d, want 401 (its own bucket)", rec.Code)
	}
}

// The strict bucket is per login and address: one client spends only its own
// budget, so it cannot lock the owner out of a second client.
func TestOneClientCannotLockTheOwnerOutForAnother(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, func(c *auth.Config) { c.LoginBurst = 3 })
	attacker := newBrowser(t, r.h)
	attacker.addr = "192.0.2.21:4000"
	for i := 1; i <= 3; i++ {
		if rec := attacker.post("/login", testLogin, testPassword, "123456"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i, rec.Code)
		}
	}
	// Same address, another spelling of the login: still the same bucket.
	if rec := attacker.post("/login", strings.ToUpper(testLogin), testPassword, "123456"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("fourth guess from one address = %d, want 429", rec.Code)
	}
	if _, rec := attacker.signup(testLogin); rec.Code != http.StatusTooManyRequests {
		t.Errorf("signup of the same login from that address = %d, want 429", rec.Code)
	}
	owner := newBrowser(t, r.h)
	owner.addr = "192.0.2.99:4000"
	if rec := owner.post("/login", testLogin, testPassword, "123456"); rec.Code != http.StatusUnauthorized {
		t.Errorf("the owner's address = %d, want 401 (its own bucket, not 429)", rec.Code)
	}
	if rec := attacker.post("/login", "someone.else@example.invalid", testPassword, "123456"); rec.Code != http.StatusUnauthorized {
		t.Errorf("another login from the attacker = %d, want 401: the limit is per login", rec.Code)
	}
}

// The ceiling is per login alone: many addresses together stay bounded.
func TestAllAddressesTogetherAreBoundedPerLogin(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, func(c *auth.Config) { c.CeilingBurst = 3 })
	for i := 1; i <= 3; i++ {
		b := newBrowser(t, r.h)
		b.addr = fmt.Sprintf("192.0.2.%d:4000", 20+i)
		if rec := b.post("/login", testLogin, testPassword, "123456"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i, rec.Code)
		}
	}
	fresh := newBrowser(t, r.h)
	fresh.addr = "192.0.2.99:4000"
	if rec := fresh.post("/login", strings.ToUpper(testLogin), testPassword, "123456"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("fourth guess against one login from a new address = %d, want 429", rec.Code)
	}
	if rec := fresh.post("/login", "someone.else@example.invalid", testPassword, "123456"); rec.Code != http.StatusUnauthorized {
		t.Errorf("another login = %d, want 401: the ceiling is per login", rec.Code)
	}
	// An hour later the ceiling has refilled.
	r.clock.Add(time.Hour)
	if rec := fresh.post("/login", testLogin, testPassword, "123456"); rec.Code != http.StatusUnauthorized {
		t.Errorf("after an hour = %d, want 401", rec.Code)
	}
}

// A login while holding a session ends that session: the old cookie must not
// stay valid next to the new one (session fixation).
func TestALoginEndsTheSessionTheRequestCarried(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	b := newBrowser(t, r.h)
	secret := r.register(t, b, testLogin)
	old := map[string]string{}
	for k, v := range b.cookies {
		old[k] = v
	}
	if len(old) != 1 {
		t.Fatalf("cookies = %v, want the session cookie", old)
	}

	if rec := r.login(t, b, testLogin, secret); rec.Code != http.StatusOK {
		t.Fatalf("login while signed in = %d %s", rec.Code, rec.Body)
	}
	if got := ownerStatus(b, http.MethodGet, "/lists", nil, ""); got != http.StatusOK {
		t.Fatalf("the new session answered %d, want 200", got)
	}
	holder := newBrowser(t, r.h)
	holder.cookies = old
	if got := ownerStatus(holder, http.MethodGet, "/lists", nil, ""); got != http.StatusUnauthorized {
		t.Errorf("the session cookie from before the login answered %d, want 401", got)
	}
	for name, value := range b.cookies {
		if old[name] == value {
			t.Errorf("the cookie %s was not replaced by the login", name)
		}
	}
}

// A signup deletes the accounts that were never confirmed within 24 hours, and
// only those.
func TestASignupDeletesStaleUnconfirmedAccountsOnly(t *testing.T) {
	t.Parallel()
	fake := newFakeBackend()
	r := newRig(t, fake, true, nil)
	fake.now = r.clock.Now

	stale, young, confirmed, withData := "stale@example.invalid", "young@example.invalid", "confirmed@example.invalid", "data@example.invalid"
	b := newBrowser(t, r.h)
	r.register(t, newBrowser(t, r.h), confirmed)
	if _, rec := b.signup(withData); rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	fake.hasData[fake.byKey[accounts.FoldLogin(withData)]] = true
	if _, rec := b.signup(stale); rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	r.clock.Add(25 * time.Hour)
	if _, rec := newBrowser(t, r.h).signup(young); rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	r.clock.Add(time.Hour)

	// Any signup runs the cleanup. The stale login is free again.
	if _, rec := newBrowser(t, r.h).signup(stale); rec.Code != http.StatusCreated {
		t.Errorf("the stale login after 24 hours = %d, want 201 (account deleted)", rec.Code)
	}
	for _, kept := range []string{young, confirmed, withData} {
		if _, rec := newBrowser(t, r.h).signup(kept); rec.Code != http.StatusConflict {
			t.Errorf("signup of %s = %d, want 409 (account kept)", kept, rec.Code)
		}
	}
}

func TestACrossSiteLoginIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	b := newBrowser(t, r.h)
	rec := b.do(http.MethodPost, auth.Prefix+"/login",
		map[string]string{"login": testLogin, "password": testPassword, "code": "123456"}, "",
		"Sec-Fetch-Site", "cross-site")
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross site login = %d, want 403", rec.Code)
	}
}

// Over https the cookie is a __Host- cookie: Secure, HttpOnly, Path=/, no Domain.
func TestTheSessionCookieHasTheHostAttributes(t *testing.T) {
	t.Parallel()
	c := &clock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	h, err := buildHandler(slog.New(slog.DiscardHandler), handlerDeps{
		owners:   ownertest.New(),
		accounts: accounts.New(newFakeBackend()),
		cfg:      config.Config{RegistrationOpen: true},
		tune:     func(cfg *auth.Config) { cfg.Now = c.Now },
	})
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, h)
	secret, rec := b.signup(testLogin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d %s", rec.Code, rec.Body)
	}
	rec = b.do(http.MethodPost, auth.Prefix+"/confirm",
		map[string]string{"login": testLogin, "password": testPassword, "code": codeAt(t, secret, c.Now())}, "")
	var cookie *http.Cookie
	for _, got := range rec.Result().Cookies() {
		if got.Value != "" {
			cookie = got
		}
	}
	if cookie == nil {
		t.Fatalf("no session cookie: %v", rec.Header())
	}
	if cookie.Name != "__Host-session" || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" ||
		cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie = %+v, want __Host-session, Secure, HttpOnly, Path=/, no Domain, SameSite=Lax", cookie)
	}
}

func TestTheSessionPageNeverHandsOutATokenToAnonymousCallers(t *testing.T) {
	t.Parallel()
	r := newRig(t, newFakeBackend(), true, nil)
	rec := newBrowser(t, r.h).do(http.MethodGet, auth.Prefix+"/session", nil, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "csrf_token") ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("anonymous session page = %d %s %v", rec.Code, rec.Body, rec.Header())
	}
}

// A check that cannot fail proves nothing. Each case weakens one part of the
// wiring and requires probe to notice.
func TestTheSecurityChecksNoticeAWeakenedWiring(t *testing.T) {
	t.Parallel()
	build := func(t *testing.T, open bool, weaken func(*webapp.Config)) rig {
		t.Helper()
		c := &clock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
		cfg, err := webappConfig(slog.New(slog.DiscardHandler), handlerDeps{
			owners:   ownertest.New(),
			accounts: accounts.New(newFakeBackend()),
			cfg:      config.Config{RegistrationOpen: open, AllowPlainHTTP: true},
			tune: func(a *auth.Config) {
				a.Now = c.Now
				a.ClientBurst, a.LoginBurst, a.CeilingBurst = 1000, 1000, 1000
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		weaken(&cfg)
		h, err := webapp.Handler(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return rig{h: h, clock: c}
	}

	t.Run("unweakened wiring is clean", func(t *testing.T) {
		t.Parallel()
		if found := probe(t, build(t, true, func(*webapp.Config) {}), false); len(found) != 0 {
			t.Fatalf("found %v", found)
		}
	})
	t.Run("CSRFExpected off", func(t *testing.T) {
		t.Parallel()
		found := probe(t, build(t, true, func(c *webapp.Config) { c.CSRFExpected = nil }), false)
		if !contains(found, "without the CSRF token answered") {
			t.Errorf("CSRF off went unnoticed: %v", found)
		}
	})
	t.Run("session check off", func(t *testing.T) {
		t.Parallel()
		found := probe(t, build(t, true, func(c *webapp.Config) { c.Auth = noSession{c.Auth} }), false)
		if !contains(found, "signed in owner request answered") {
			t.Errorf("session check off went unnoticed: %v", found)
		}
	})
	t.Run("a resolver that trusts everybody", func(t *testing.T) {
		t.Parallel()
		found := probe(t, build(t, true, func(c *webapp.Config) {
			c.Owner = func(*http.Request) (int64, bool) { return 1, true }
		}), false)
		if !contains(found, "was not refused with 401") {
			t.Errorf("an open resolver went unnoticed: %v", found)
		}
	})
	t.Run("registration closed ignored", func(t *testing.T) {
		t.Parallel()
		// The setting says closed, the sign in is built open.
		c := &clock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
		h, err := buildHandler(slog.New(slog.DiscardHandler), handlerDeps{
			owners:   ownertest.New(),
			accounts: accounts.New(newFakeBackend()),
			cfg:      config.Config{RegistrationOpen: false, AllowPlainHTTP: true},
			tune: func(a *auth.Config) {
				a.Now = c.Now
				a.RegistrationOpen = true
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		found := probe(t, rig{h: h, clock: c}, true)
		if !contains(found, "registration is closed but signup answered") {
			t.Errorf("ignoring the setting went unnoticed: %v", found)
		}
	})
}

// noSession is an Auth whose session middleware never reads a session.
type noSession struct{ webapp.Auth }

func (noSession) SessionMiddleware(next http.Handler) http.Handler { return next }

func contains(found []string, fragment string) bool {
	for _, f := range found {
		if strings.Contains(f, fragment) {
			return true
		}
	}
	return false
}
