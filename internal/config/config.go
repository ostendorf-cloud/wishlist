// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package config loads and validates this service's own environment once,
// in main, aggregating every problem into one error.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/analytics"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/envconf"
	"github.com/ostendorf-cloud/govue/logging"
)

// Config is every setting this binary reads from the environment.
type Config struct {
	// Addr is the public HTTP listener's address.
	Addr string
	// AdminAddr is govue's admin listener's address: /healthz, /readyz and
	// /metrics, reached only from inside the cluster, never through the
	// chart's own Ingress.
	AdminAddr string
	LogLevel  slog.Level
	// Database is the PostgreSQL connection.
	Database Database
	// RegistrationOpen lets anybody create an owner account. Off by default.
	RegistrationOpen bool
	// AllowPlainHTTP drops the Secure attribute and the __Host- prefix of the
	// session cookie, for development over http. Refused unless the listener
	// is loopback, see Load.
	AllowPlainHTTP bool
	// TrustedProxies are the peers whose X-Forwarded-For is believed. Empty
	// trusts nothing: every limit then keys by the connection address.
	TrustedProxies []netip.Prefix
	// SiteURL is the public origin of the site. Its host is what the page
	// view counting reports as the hostname of every view.
	SiteURL string
	// Umami is govue's page view counting. It is off unless UMAMI_URL and
	// UMAMI_WEBSITE_ID are both set; one of the two alone is an error.
	Umami analytics.Config
}

// defaultSiteURL is the public origin when SITE_URL is not set.
const defaultSiteURL = "https://my-wishes.de"

// SiteHostname is the bare host of SiteURL, without scheme, port or path. It
// is empty when SiteURL is not a valid origin, which Load refuses.
func (c Config) SiteHostname() string {
	u, err := url.Parse(c.SiteURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// checkSiteURL refuses anything but an origin: an http or https URL with a
// host, no credentials, no query, no fragment and no path. The error never
// repeats the value.
func checkSiteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("envconf: SITE_URL: must be a URL such as https://my-wishes.de")
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return errors.New("envconf: SITE_URL: must be an http or https origin without credentials, path, query or fragment")
	}
	return nil
}

// loopbackListener says whether addr binds the loopback interface only. An
// empty host, 0.0.0.0 and :: bind every interface and are not loopback.
func loopbackListener(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// Database is the PostgreSQL connection. It is built from separate fields,
// never from one connection string, so the password stays a secret of its own
// with a _FILE twin and never travels inside a URL that gets logged.
type Database struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
	// SSLMode is one of the values libpq knows.
	SSLMode string
	// MaxConns caps the pool. Several pods share one server, so the cap is
	// per pod.
	MaxConns int32
	// ConnectTimeout bounds one attempt to open a connection.
	ConnectTimeout time.Duration
}

// redacted is what stands in for the password wherever a Database is printed.
const redacted = "[redacted]"

// String prints the connection without its password, so a "%v" in a log line
// or an error message can never leak it.
func (d Database) String() string {
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		d.Host, d.Port, d.Name, d.User, redacted, d.SSLMode)
}

// GoString keeps "%#v" and "%+v" from printing the password field either.
func (d Database) GoString() string { return d.String() }

// Format makes every fmt verb print String, including "%+v" and "%#v".
func (d Database) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, d.String())
}

// MarshalJSON keeps the password out of JSON. It matters when a whole Config
// is logged through a JSON handler: slog's LogValuer applies only when a
// Database is the attribute value itself, while a Database nested inside
// another value reaches encoding/json, which would print every field.
func (d Database) MarshalJSON() ([]byte, error) {
	// plain has Database's fields and none of its methods, so this does not
	// call MarshalJSON again.
	type plain Database
	p := plain(d)
	if p.Password != "" {
		p.Password = redacted
	}
	return json.Marshal(p)
}

// MarshalText keeps the password out of every other text encoding.
func (d Database) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// LogValue keeps the password out of structured logs.
func (d Database) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", d.Host),
		slog.Int("port", d.Port),
		slog.String("name", d.Name),
		slog.String("user", d.User),
		slog.String("sslmode", d.SSLMode),
	)
}

// sslModes are the values PostgreSQL's sslmode parameter accepts.
var sslModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// Load reads every setting through envconf, so a misconfigured deployment
// reports every mistake on the first start instead of one per restart.
//
// Every secret-shaped variable is read through envconf's SecretOrFile
// family, which gives it a _FILE twin: DB_PASSWORD or DB_PASSWORD_FILE.
func Load(getenv func(string) string) (Config, error) {
	l := envconf.New(getenv)

	cfg := Config{
		Addr:      l.String("ADDR", ":8080"),
		AdminAddr: l.String("ADMIN_ADDR", ":8081"),
	}

	var problems []error
	level, err := logging.Level(l.String("LOG_LEVEL", "info"))
	if err != nil {
		problems = append(problems, err)
	}
	cfg.LogLevel = level

	cfg.Database = Database{
		Host:           l.Required("DB_HOST"),
		Port:           l.Int("DB_PORT", 5432, 1, 65535),
		Name:           l.Required("DB_NAME"),
		User:           l.Required("DB_USER"),
		Password:       l.RequiredSecretOrFile("DB_PASSWORD"),
		SSLMode:        l.String("DB_SSLMODE", "require"),
		MaxConns:       int32(l.Int("DB_MAX_CONNS", 10, 1, 100)),
		ConnectTimeout: l.Duration("DB_CONNECT_TIMEOUT", 5*time.Second, time.Second, time.Minute),
	}
	if !slices.Contains(sslModes, cfg.Database.SSLMode) {
		problems = append(problems, fmt.Errorf("envconf: DB_SSLMODE: must be one of %v, got %q", sslModes, cfg.Database.SSLMode))
	}

	cfg.RegistrationOpen = l.Bool("REGISTRATION_OPEN", false)

	proxies, err := clientip.ParseCIDRs(l.String("TRUSTED_PROXIES", ""))
	if err != nil {
		problems = append(problems, fmt.Errorf("envconf: TRUSTED_PROXIES: %w", err))
	}
	cfg.TrustedProxies = proxies

	cfg.SiteURL = strings.TrimRight(l.String("SITE_URL", defaultSiteURL), "/")
	if err := checkSiteURL(cfg.SiteURL); err != nil {
		problems = append(problems, err)
	}

	// Page view counting is off unless both UMAMI_URL and UMAMI_WEBSITE_ID
	// are set. LoadConfig returns every problem of the pair at once and never
	// repeats the URL.
	umami, err := analytics.LoadConfig(getenv)
	if err != nil {
		problems = append(problems, err)
	}
	cfg.Umami = umami

	// A session cookie over plain http travels in the clear, so it is opt in
	// twice. The first variable turns it on. It is then honoured only when the
	// public listener is loopback, or when the operator states that the
	// listener is reachable from the loopback interface only by other means:
	// docker compose binds the container to every interface and publishes the
	// port on 127.0.0.1. The Helm chart passes neither variable.
	cfg.AllowPlainHTTP = l.Bool("WISHLIST_ALLOW_PLAIN_HTTP", false)
	anyListener := l.Bool("WISHLIST_ALLOW_PLAIN_HTTP_ANY_LISTENER", false)
	if cfg.AllowPlainHTTP {
		if !anyListener && !loopbackListener(cfg.Addr) {
			problems = append(problems, fmt.Errorf("envconf: WISHLIST_ALLOW_PLAIN_HTTP: refused, ADDR %q is not a loopback address; "+
				"set WISHLIST_ALLOW_PLAIN_HTTP_ANY_LISTENER=true only where the port is published on 127.0.0.1", cfg.Addr))
		}
	} else if anyListener {
		problems = append(problems, errors.New("envconf: WISHLIST_ALLOW_PLAIN_HTTP_ANY_LISTENER: has no effect without WISHLIST_ALLOW_PLAIN_HTTP"))
	}

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
