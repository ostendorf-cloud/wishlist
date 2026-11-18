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
	"slices"
	"time"

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

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
