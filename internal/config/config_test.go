// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dummyPassword is an obvious fake. Tests search for it in output to prove
// it never leaks.
const dummyPassword = "dummy-password-not-real"

// env builds a getenv function over a fixed map, the shape Load expects. A
// key absent from the map behaves exactly like an unset environment
// variable: envconf's own lookup returns "".
func env(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

// validEnv is the smallest environment Load accepts.
func validEnv() map[string]string {
	return map[string]string{
		"DB_HOST":     "db.example.invalid",
		"DB_NAME":     "wishlist",
		"DB_USER":     "wishlist",
		"DB_PASSWORD": dummyPassword,
	}
}

// with copies base and applies changes, an empty value deleting the key.
func with(base, changes map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range changes {
		if v == "" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	return path
}

func TestLoadAppliesDefaultsWhenOnlyTheRequiredVariablesAreSet(t *testing.T) {
	t.Parallel()

	cfg, err := Load(env(validEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":8080")
	}
	if cfg.AdminAddr != ":8081" {
		t.Errorf("AdminAddr = %q, want %q", cfg.AdminAddr, ":8081")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
	want := Database{
		Host: "db.example.invalid", Port: 5432, Name: "wishlist", User: "wishlist",
		Password: dummyPassword, SSLMode: "require", MaxConns: 10, ConnectTimeout: 5 * time.Second,
	}
	if cfg.Database != want {
		t.Errorf("Database = %#v, want %#v", cfg.Database, want)
	}
}

func TestLoadOverridesEachVariable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		env   map[string]string
		check func(t *testing.T, cfg Config)
	}{
		{"ADDR", map[string]string{"ADDR": ":9090"}, func(t *testing.T, c Config) {
			if c.Addr != ":9090" {
				t.Errorf("Addr = %q", c.Addr)
			}
		}},
		{"ADMIN_ADDR", map[string]string{"ADMIN_ADDR": ":9091"}, func(t *testing.T, c Config) {
			if c.AdminAddr != ":9091" {
				t.Errorf("AdminAddr = %q", c.AdminAddr)
			}
		}},
		{"LOG_LEVEL", map[string]string{"LOG_LEVEL": "debug"}, func(t *testing.T, c Config) {
			if c.LogLevel != slog.LevelDebug {
				t.Errorf("LogLevel = %v", c.LogLevel)
			}
		}},
		{"DB_PORT", map[string]string{"DB_PORT": "6432"}, func(t *testing.T, c Config) {
			if c.Database.Port != 6432 {
				t.Errorf("Port = %d", c.Database.Port)
			}
		}},
		{"DB_SSLMODE", map[string]string{"DB_SSLMODE": "disable"}, func(t *testing.T, c Config) {
			if c.Database.SSLMode != "disable" {
				t.Errorf("SSLMode = %q", c.Database.SSLMode)
			}
		}},
		{"DB_MAX_CONNS", map[string]string{"DB_MAX_CONNS": "25"}, func(t *testing.T, c Config) {
			if c.Database.MaxConns != 25 {
				t.Errorf("MaxConns = %d", c.Database.MaxConns)
			}
		}},
		{"DB_CONNECT_TIMEOUT", map[string]string{"DB_CONNECT_TIMEOUT": "9s"}, func(t *testing.T, c Config) {
			if c.Database.ConnectTimeout != 9*time.Second {
				t.Errorf("ConnectTimeout = %v", c.Database.ConnectTimeout)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := Load(env(with(validEnv(), tc.env)))
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			tc.check(t, cfg)
		})
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		changes map[string]string
		want    string
	}{
		{"log level", map[string]string{"LOG_LEVEL": "verbose"}, "verbose"},
		{"missing host", map[string]string{"DB_HOST": ""}, "DB_HOST"},
		{"missing name", map[string]string{"DB_NAME": ""}, "DB_NAME"},
		{"missing user", map[string]string{"DB_USER": ""}, "DB_USER"},
		{"missing password", map[string]string{"DB_PASSWORD": ""}, "DB_PASSWORD"},
		{"port not a number", map[string]string{"DB_PORT": "http"}, "DB_PORT"},
		{"port zero", map[string]string{"DB_PORT": "0"}, "DB_PORT"},
		{"port too large", map[string]string{"DB_PORT": "70000"}, "DB_PORT"},
		{"unknown sslmode", map[string]string{"DB_SSLMODE": "maybe"}, "DB_SSLMODE"},
		{"pool size zero", map[string]string{"DB_MAX_CONNS": "0"}, "DB_MAX_CONNS"},
		{"pool size too large", map[string]string{"DB_MAX_CONNS": "101"}, "DB_MAX_CONNS"},
		{"timeout not a duration", map[string]string{"DB_CONNECT_TIMEOUT": "soon"}, "DB_CONNECT_TIMEOUT"},
		{"timeout too short", map[string]string{"DB_CONNECT_TIMEOUT": "10ms"}, "DB_CONNECT_TIMEOUT"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(env(with(validEnv(), tc.changes)))
			if err == nil {
				t.Fatal("Load() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), "config:") {
				t.Errorf("error %q is not wrapped with \"config:\"", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s", err, tc.want)
			}
		})
	}
}

// Every mistake has to come back in one error, not one per restart.
func TestLoadAggregatesEveryProblemIntoOneError(t *testing.T) {
	t.Parallel()

	_, err := Load(env(map[string]string{
		"LOG_LEVEL":    "verbose",
		"DB_PORT":      "0",
		"DB_SSLMODE":   "maybe",
		"DB_MAX_CONNS": "0",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	for _, want := range []string{
		"verbose", "DB_HOST", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_PORT", "DB_SSLMODE", "DB_MAX_CONNS",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregated error does not mention %s:\n%v", want, err)
		}
	}
}

// secretVariables lists every secret-shaped variable Load reads. A new one
// is added here and gets the _FILE twin checks below for free.
var secretVariables = []struct {
	name string
	get  func(Config) string
}{
	{"DB_PASSWORD", func(c Config) string { return c.Database.Password }},
}

func TestEverySecretHasAFileTwin(t *testing.T) {
	t.Parallel()

	for _, secret := range secretVariables {
		t.Run(secret.name+" from file", func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, "  from-a-file\n")
			cfg, err := Load(env(with(validEnv(), map[string]string{secret.name: "", secret.name + "_FILE": path})))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := secret.get(cfg); got != "from-a-file" {
				t.Errorf("value = %q, want the trimmed file content", got)
			}
		})

		t.Run(secret.name+" direct and file", func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, "x")
			_, err := Load(env(with(validEnv(), map[string]string{secret.name + "_FILE": path})))
			if err == nil || !strings.Contains(err.Error(), secret.name+"_FILE") {
				t.Errorf("error = %v, want one naming %s_FILE for setting both", err, secret.name)
			}
		})

		t.Run(secret.name+" empty file", func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, "\n")
			_, err := Load(env(with(validEnv(), map[string]string{secret.name: "", secret.name + "_FILE": path})))
			if err == nil || !strings.Contains(err.Error(), "empty") {
				t.Errorf("error = %v, want one about an empty file", err)
			}
		})

		t.Run(secret.name+" missing file", func(t *testing.T) {
			t.Parallel()

			missing := filepath.Join(t.TempDir(), "absent")
			_, err := Load(env(with(validEnv(), map[string]string{secret.name: "", secret.name + "_FILE": missing})))
			if err == nil || !strings.Contains(err.Error(), "cannot read") {
				t.Errorf("error = %v, want one about an unreadable file", err)
			}
		})
	}
}

func TestDatabaseNeverPrintsItsPassword(t *testing.T) {
	t.Parallel()

	cfg, err := Load(env(validEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if out := fmt.Sprintf(verb, cfg.Database); strings.Contains(out, dummyPassword) {
			t.Errorf("Sprintf(%q) leaks the password: %s", verb, out)
		}
		if out := fmt.Sprintf(verb, cfg); strings.Contains(out, dummyPassword) {
			t.Errorf("Sprintf(%q) of the whole Config leaks the password: %s", verb, out)
		}
	}

	// Both handlers, with the Database alone and with the whole Config: a
	// Database nested in a Config does not go through LogValue.
	handlers := map[string]func(io.Writer) slog.Handler{
		"text": func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, nil) },
		"json": func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, nil) },
	}
	for name, newHandler := range handlers {
		for what, value := range map[string]any{"database": cfg.Database, "config": cfg, "config pointer": &cfg} {
			var sb strings.Builder
			slog.New(newHandler(&sb)).Info("startup", "value", value)
			if strings.Contains(sb.String(), dummyPassword) {
				t.Errorf("%s handler leaks the password logging the %s: %s", name, what, sb.String())
			}
		}
	}

	// MarshalText is what every text encoder reaches for: map keys, flags,
	// TOML and YAML libraries. It has to be redacted on its own.
	var marshaler encoding.TextMarshaler = cfg.Database
	text, err := marshaler.MarshalText()
	if err != nil || strings.Contains(string(text), dummyPassword) {
		t.Errorf("MarshalText() = %q, %v, want it without the password", text, err)
	}
	for what, value := range map[string]any{"database": cfg.Database, "config": cfg} {
		raw, err := xml.Marshal(value)
		if err != nil {
			t.Fatalf("xml.Marshal(%s) error = %v", what, err)
		}
		if strings.Contains(string(raw), dummyPassword) {
			t.Errorf("xml.Marshal leaks the password of the %s: %s", what, raw)
		}
	}

	for what, value := range map[string]any{"database": cfg.Database, "config": cfg} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("Marshal(%s) error = %v", what, err)
		}
		if strings.Contains(string(raw), dummyPassword) {
			t.Errorf("json.Marshal leaks the password of the %s: %s", what, raw)
		}
	}
}
