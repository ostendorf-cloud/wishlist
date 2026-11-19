// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

func TestRegistrationIsClosedByDefaultAndOpenedBySetting(t *testing.T) {
	cfg, err := Load(env(validEnv()))
	if err != nil || cfg.RegistrationOpen {
		t.Fatalf("default: RegistrationOpen = %v, err %v, want closed", cfg.RegistrationOpen, err)
	}
	cfg, err = Load(env(with(validEnv(), map[string]string{"REGISTRATION_OPEN": "true"})))
	if err != nil || !cfg.RegistrationOpen {
		t.Fatalf("REGISTRATION_OPEN=true: RegistrationOpen = %v, err %v, want open", cfg.RegistrationOpen, err)
	}
	if _, err = Load(env(with(validEnv(), map[string]string{"REGISTRATION_OPEN": "maybe"}))); err == nil {
		t.Error("REGISTRATION_OPEN=maybe was accepted")
	}
}

func TestTrustedProxiesAreEmptyByDefaultAndParsed(t *testing.T) {
	cfg, err := Load(env(validEnv()))
	if err != nil || len(cfg.TrustedProxies) != 0 {
		t.Fatalf("default: %v, %v, want none", cfg.TrustedProxies, err)
	}
	cfg, err = Load(env(with(validEnv(), map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8, 192.0.2.1"})))
	if err != nil || len(cfg.TrustedProxies) != 2 {
		t.Fatalf("two entries: %v, %v", cfg.TrustedProxies, err)
	}
	if _, err = Load(env(with(validEnv(), map[string]string{"TRUSTED_PROXIES": "not-a-network"}))); err == nil {
		t.Error("a bad entry was accepted")
	}
}

func TestPlainHTTPCookiesAreHonouredOnlyOnALoopbackListener(t *testing.T) {
	const on = "WISHLIST_ALLOW_PLAIN_HTTP"
	cases := []struct {
		name    string
		changes map[string]string
		want    string // "" means accepted, otherwise a fragment of the error
	}{
		{"loopback ipv4", map[string]string{on: "true", "ADDR": "127.0.0.1:8080"}, ""},
		{"loopback ipv6", map[string]string{on: "true", "ADDR": "[::1]:8080"}, ""},
		{"localhost", map[string]string{on: "true", "ADDR": "localhost:8080"}, ""},
		{"the default listener", map[string]string{on: "true"}, "not a loopback"},
		{"every interface", map[string]string{on: "true", "ADDR": "0.0.0.0:8080"}, "not a loopback"},
		{"a public address", map[string]string{on: "true", "ADDR": "10.0.0.5:8080"}, "not a loopback"},
		{"compose acknowledges it", map[string]string{on: "true", on + "_ANY_LISTENER": "true"}, ""},
		{"the acknowledgement alone", map[string]string{on + "_ANY_LISTENER": "true"}, "no effect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(env(with(validEnv(), tc.changes)))
			if tc.want == "" {
				if err != nil || !cfg.AllowPlainHTTP {
					t.Errorf("Load() = %v, %v, want it accepted", cfg.AllowPlainHTTP, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load() error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestPlainHTTPCookiesAreOffByDefault(t *testing.T) {
	cfg, err := Load(env(validEnv()))
	if err != nil || cfg.AllowPlainHTTP {
		t.Errorf("AllowPlainHTTP = %v, err %v, want off", cfg.AllowPlainHTTP, err)
	}
}
