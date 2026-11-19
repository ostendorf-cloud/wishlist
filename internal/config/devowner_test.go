// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

func TestDevOwnerIsOffByDefault(t *testing.T) {
	cfg, err := Load(env(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DevOwner != "" {
		t.Errorf("DevOwner = %q, want it off", cfg.DevOwner)
	}
}

func TestDevOwnerIsHonouredOnlyOnALoopbackListener(t *testing.T) {
	cases := []struct {
		name    string
		changes map[string]string
		want    string // "" means accepted, otherwise a fragment of the error
	}{
		{"loopback ipv4", map[string]string{"WISHLIST_DEV_OWNER": "dev", "ADDR": "127.0.0.1:8080"}, ""},
		{"loopback ipv6", map[string]string{"WISHLIST_DEV_OWNER": "dev", "ADDR": "[::1]:8080"}, ""},
		{"localhost", map[string]string{"WISHLIST_DEV_OWNER": "dev", "ADDR": "localhost:8080"}, ""},
		{"the default listener", map[string]string{"WISHLIST_DEV_OWNER": "dev"}, "not a loopback"},
		{"every interface", map[string]string{"WISHLIST_DEV_OWNER": "dev", "ADDR": "0.0.0.0:8080"}, "not a loopback"},
		{"a public address", map[string]string{"WISHLIST_DEV_OWNER": "dev", "ADDR": "10.0.0.5:8080"}, "not a loopback"},
		{"compose acknowledges it", map[string]string{"WISHLIST_DEV_OWNER": "dev", "WISHLIST_DEV_OWNER_ANY_LISTENER": "true"}, ""},
		{"the acknowledgement alone", map[string]string{"WISHLIST_DEV_OWNER_ANY_LISTENER": "true"}, "no effect"},
		{"bad name", map[string]string{"WISHLIST_DEV_OWNER": "Dev Owner", "ADDR": "127.0.0.1:8080"}, "a-z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(env(with(validEnv(), tc.changes)))
			if tc.want == "" {
				if err != nil || cfg.DevOwner != "dev" {
					t.Errorf("Load() = %q, %v, want it accepted", cfg.DevOwner, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load() error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
