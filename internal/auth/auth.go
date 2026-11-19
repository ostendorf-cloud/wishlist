// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package auth is the owner's sign in: govue's account service and handlers
// over wishlist's own tables, a cookie session, the CSRF token bound to it and
// the rate limits of the routes that check a password.
//
// It is the one place that connects govue's account, session and csrf packages,
// which import nothing from each other. Nothing here is built that govue
// provides, except two small things it does not: the page that tells the SPA
// who is signed in and which CSRF token to send, and the rate limits per login.
//
// The session store is in process memory (session.MemoryStore). A restart
// signs every owner out, and a second replica would not see the sessions of
// the first: the chart pins replicaCount to 1 for that reason. Scaling out
// needs a shared session.Store first.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/ratelimit"
	"github.com/ostendorf-cloud/govue/session"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
)

// Prefix is where the account routes live.
const Prefix = "/api/v1/account"

// subjectPrefix keeps the subject of a session of an own account apart from
// the subject of any later OIDC login: both share the namespace EndAll keys on.
const subjectPrefix = "local:"

// issuer is the name an authenticator app groups the entry under.
const issuer = "Wishlist"

// Limits. The client limit is per address. The login limit is per login and
// address, so one address cannot use up the budget of another. The ceiling is
// per login alone and generous: it bounds guesses from many addresses without
// letting a few requests lock the owner out. All count every attempt, a
// success too.
const (
	DefaultClientBurst = 5
	DefaultClientPer   = time.Minute
	DefaultLoginBurst  = 10
	DefaultLoginPer    = 15 * time.Minute

	DefaultCeilingBurst = 100
	DefaultCeilingPer   = time.Hour

	maxSessions = 10_000
	maxKeys     = 100_000
)

// ErrRegistrationClosed is the reason the signup handler gets when registration
// is off. govue answers it as 403 "signup is not available".
var ErrRegistrationClosed = errors.New("registration is closed")

// Config configures Auth.
type Config struct {
	// Accounts is the account store. Required.
	Accounts account.Store
	// ClientIPs resolves the address the limits key by. Required.
	ClientIPs *clientip.Resolver
	Logger    *slog.Logger
	// RegistrationOpen allows signup.
	RegistrationOpen bool
	// AllowPlainHTTP drops the Secure attribute and the __Host- prefix of the
	// session cookie. Development on a loopback address only.
	AllowPlainHTTP bool
	// Zero values take the defaults above.
	ClientBurst int
	ClientPer   time.Duration
	LoginBurst  int
	LoginPer    time.Duration
	// CeilingBurst and CeilingPer bound all attempts on one login, from any
	// address.
	CeilingBurst int
	CeilingPer   time.Duration
	// Now is the clock. Nil is time.Now. Tests set it.
	Now func() time.Time
}

// Auth holds the parts of the sign in.
type Auth struct {
	sessions *session.Manager
	handlers *account.Handlers
	csrfKey  []byte
	open     bool

	clientIPs *clientip.Resolver

	clientLimit  *middleware.RateLimit
	loginLimit   *ratelimit.Limiter
	ceilingLimit *ratelimit.Limiter
}

// New builds the sign in. The CSRF key is random per process: the sessions it
// protects live in this process only, so no key has to survive a restart.
func New(cfg Config) (*Auth, error) {
	if cfg.Accounts == nil {
		return nil, errors.New("auth: accounts store is required")
	}
	if cfg.ClientIPs == nil {
		return nil, errors.New("auth: client address resolver is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	cfg.ClientBurst = orInt(cfg.ClientBurst, DefaultClientBurst)
	cfg.LoginBurst = orInt(cfg.LoginBurst, DefaultLoginBurst)
	cfg.ClientPer = orDuration(cfg.ClientPer, DefaultClientPer)
	cfg.LoginPer = orDuration(cfg.LoginPer, DefaultLoginPer)
	cfg.CeilingBurst = orInt(cfg.CeilingBurst, DefaultCeilingBurst)
	cfg.CeilingPer = orDuration(cfg.CeilingPer, DefaultCeilingPer)

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("auth: csrf key: %w", err)
	}

	sessions, err := session.New(session.Config{
		Store:          session.NewMemoryStore(now, maxSessions),
		AllowPlainHTTP: cfg.AllowPlainHTTP,
		Now:            now,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	hasher, err := account.NewHasher(account.HasherConfig{})
	if err != nil {
		return nil, fmt.Errorf("auth: password hasher: %w", err)
	}
	service, err := account.NewService(account.ServiceConfig{
		Store:  cfg.Accounts,
		Hasher: hasher,
		Issuer: issuer,
		// No password change route is mounted yet, but the contract wants the
		// callback, and it must end every session of the account.
		EndSessions: func(ctx context.Context, accountID string) error {
			return sessions.EndAll(ctx, subjectPrefix+accountID)
		},
		Logger: logger,
		Now:    now,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	a := &Auth{sessions: sessions, csrfKey: key, open: cfg.RegistrationOpen, clientIPs: cfg.ClientIPs}

	handlers, err := account.NewHandlers(account.HandlerConfig{
		Service: service,
		StartSession: func(ctx context.Context, w http.ResponseWriter, r *http.Request, id account.Identity) error {
			// A login does not end the session the request carries: end it
			// first, and fail closed so there is never a second live session.
			if err := sessions.End(ctx, w, r); err != nil {
				return err
			}
			_, err := sessions.Start(ctx, w, session.Session{Subject: subjectPrefix + id.ID, Username: id.Login})
			return err
		},
		EndSession: func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
			return sessions.End(ctx, w, r)
		},
		CurrentIdentity: func(r *http.Request) (account.Identity, bool, error) {
			if err := session.ErrorFromContext(r.Context()); err != nil {
				return account.Identity{}, false, err
			}
			s, ok := session.FromContext(r.Context())
			if !ok {
				return account.Identity{}, false, nil
			}
			return account.Identity{ID: strings.TrimPrefix(s.Subject, subjectPrefix), Login: s.Username}, true, nil
		},
		AllowSignup: func(*http.Request) error {
			if !a.open {
				return ErrRegistrationClosed
			}
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	a.handlers = handlers

	a.clientLimit, err = middleware.NewRateLimit(middleware.RateLimitConfig{
		Limiter:  ratelimit.New(cfg.ClientBurst, cfg.ClientPer, maxKeys),
		ClientIP: cfg.ClientIPs,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	a.loginLimit = ratelimit.New(cfg.LoginBurst, cfg.LoginPer, maxKeys)
	a.loginLimit.Now = now
	a.ceilingLimit = ratelimit.New(cfg.CeilingBurst, cfg.CeilingPer, maxKeys)
	a.ceilingLimit.Now = now
	return a, nil
}

func orInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func orDuration(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}

// SessionMiddleware reads the session of a request into its context. It sits
// after the CSRF protector in the route chain.
func (a *Auth) SessionMiddleware(next http.Handler) http.Handler {
	return a.sessions.Middleware(next)
}

// ClientLimit is the stricter per address limit of the routes that check a
// password.
func (a *Auth) ClientLimit(next http.Handler) http.Handler {
	return a.clientLimit.Middleware(next)
}

// CSRFExpected derives the token a request must carry from its session. It
// answers false for a request without a session, which carries no authority a
// forged request could borrow, and for a store that could not answer.
func (a *Auth) CSRFExpected() csrf.Expected {
	return func(r *http.Request) (string, bool) {
		s, ok, err := a.sessions.Read(r.Context(), r)
		if err != nil || !ok {
			return "", false
		}
		return csrf.Token(a.csrfKey, s.ID), true
	}
}

// OwnerID is the owner resolver: the owners row id of the signed in account.
// A request without a session, with a session whose subject is not an own
// account, or with an id that is not a plain row id is nobody.
func (a *Auth) OwnerID(r *http.Request) (int64, bool) {
	s, ok := session.FromContext(r.Context())
	if !ok {
		return 0, false
	}
	id, found := strings.CutPrefix(s.Subject, subjectPrefix)
	if !found {
		return 0, false
	}
	return accounts.OwnerID(id)
}

// Mount registers the account routes. route is the chain every route runs in.
// checked adds the stricter client limit and is used for the routes that cost
// a password derivation. Signup, login and confirmation also get the limit per
// login.
func (a *Auth) Mount(mux *http.ServeMux, route, checked func(http.Handler) http.Handler) {
	perLogin := func(h http.Handler) http.Handler { return checked(a.limitByLogin(h)) }
	mux.Handle("POST "+Prefix+"/signup", perLogin(a.handlers.Signup()))
	mux.Handle("POST "+Prefix+"/confirm", perLogin(a.handlers.ConfirmTOTP()))
	mux.Handle("POST "+Prefix+"/login", perLogin(a.handlers.Login()))
	mux.Handle("POST "+Prefix+"/logout", route(a.handlers.Logout()))
	mux.Handle("GET "+Prefix+"/session", route(api.Handle(a.whoami)))
}

// Info is the answer of GET /session: who is signed in, the CSRF token of that
// session, and whether the sign up form is worth showing.
type Info struct {
	SignedIn         bool   `json:"signed_in"`
	Login            string `json:"login,omitempty"`
	CSRFToken        string `json:"csrf_token,omitempty"`
	RegistrationOpen bool   `json:"registration_open"`
}

// whoami answers 200 for everybody, so a page without a session costs no 401.
// The token goes to the page that holds the session and nowhere else: a cross
// site page cannot read this answer, and the session cookie is SameSite Lax.
func (a *Auth) whoami(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	if err := session.ErrorFromContext(r.Context()); err != nil {
		return api.Unavailable("the session store is not available")
	}
	info := Info{RegistrationOpen: a.open}
	if s, ok := session.FromContext(r.Context()); ok {
		info.SignedIn = true
		info.Login = s.Username
		info.CSRFToken = csrf.Token(a.csrfKey, s.ID)
	}
	return api.Write(w, http.StatusOK, info)
}

// limitByLogin takes one token per request from the buckets of the login in the
// body, before the handler spends a password derivation on it. It reads the
// body the way the handler does (encoding/json, so the same case insensitive
// field match) and puts it back. A body it cannot read a login from is left to
// the handler. govue runs one Argon2id derivation for an unknown or empty
// login, so only the client limit covers that case.
//
// Two buckets, both shared by signup, login and confirmation, which together
// are the guesses against one account. The strict one is keyed on the login
// and the client address: one address spends only its own budget and cannot
// lock the owner out of another. The ceiling is keyed on the login alone and is
// far above what an owner types, so guesses from many addresses stay bounded.
// The cost of the ceiling is that a determined crowd can still make the owner
// wait until it refills.
func (a *Auth) limitByLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		limit := account.DefaultMaxBodyBytes
		head, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		original := r.Body
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head), original), original}
		if err == nil && int64(len(head)) <= limit {
			var probe struct {
				Login string `json:"login"`
			}
			if json.Unmarshal(head, &probe) == nil && probe.Login != "" {
				sum := sha256.Sum256([]byte(accounts.FoldLogin(probe.Login)))
				login := hex.EncodeToString(sum[:])
				client := clientip.Key(a.clientIPs.Resolve(r))
				if ok, wait := a.loginLimit.Take(login + "|" + client); !ok {
					api.WriteError(w, r, api.TooManyRequests(wait))
					return
				}
				if ok, wait := a.ceilingLimit.Take(login); !ok {
					api.WriteError(w, r, api.TooManyRequests(wait))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
