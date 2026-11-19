// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"net/netip"
	"strings"
	"testing"
)

const testWebsiteID = "0a1b2c3d-0000-4000-8000-000000000001"

func TestCountingIsOffAndTheSiteIsMyWishesByDefault(t *testing.T) {
	t.Parallel()

	cfg, err := Load(env(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Umami.Enabled() {
		t.Error("counting must be off without UMAMI_URL and UMAMI_WEBSITE_ID")
	}
	if cfg.SiteURL != "https://my-wishes.de" || cfg.SiteHostname() != "my-wishes.de" {
		t.Errorf("SiteURL = %q, hostname %q", cfg.SiteURL, cfg.SiteHostname())
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want none", cfg.TrustedProxies)
	}
}

func TestCountingNeedsBothValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		changes map[string]string
		wantErr string // "" means accepted
		enabled bool
	}{
		{"both", map[string]string{"UMAMI_URL": "https://umami.example.test", "UMAMI_WEBSITE_ID": testWebsiteID}, "", true},
		{"url alone", map[string]string{"UMAMI_URL": "https://umami.example.test"}, "UMAMI_", false},
		{"id alone", map[string]string{"UMAMI_WEBSITE_ID": testWebsiteID}, "UMAMI_", false},
		{"id that is no uuid", map[string]string{"UMAMI_URL": "https://umami.example.test", "UMAMI_WEBSITE_ID": "nope"}, "UMAMI_WEBSITE_ID", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := Load(env(with(validEnv(), tc.changes)))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}
				if cfg.Umami.Enabled() != tc.enabled {
					t.Errorf("Enabled = %v, want %v", cfg.Umami.Enabled(), tc.enabled)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() error = %v, want it to name %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "umami.example.test") {
				t.Errorf("the error repeats the Umami URL: %v", err)
			}
		})
	}
}

func TestSiteURLIsAnOriginAndGivesTheBareHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw      string
		wantHost string // "" means refused
	}{
		{"https://wishes.example.test", "wishes.example.test"},
		{"https://wishes.example.test/", "wishes.example.test"},
		{"https://wishes.example.test:8443", "wishes.example.test"},
		{"http://localhost:8080", "localhost"},
		{"wishes.example.test", ""},
		{"ftp://wishes.example.test", ""},
		{"https://user:pw@wishes.example.test", ""},
		{"https://wishes.example.test/app", ""},
		{"https://wishes.example.test?x=1", ""},
		{"https://wishes.example.test#x", ""},
		{"https://", ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			cfg, err := Load(env(with(validEnv(), map[string]string{"SITE_URL": tc.raw})))
			if tc.wantHost == "" {
				if err == nil || !strings.Contains(err.Error(), "SITE_URL") {
					t.Fatalf("Load() error = %v, want SITE_URL refused", err)
				}
				if strings.Contains(err.Error(), "pw@") {
					t.Errorf("the error repeats the credentials: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := cfg.SiteHostname(); got != tc.wantHost {
				t.Errorf("SiteHostname() = %q, want %q", got, tc.wantHost)
			}
		})
	}
}

func TestTrustedProxiesAreParsedAndABadOneIsRefused(t *testing.T) {
	t.Parallel()

	cfg, err := Load(env(with(validEnv(), map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8, 192.0.2.1"})))
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.1/32")}
	if len(cfg.TrustedProxies) != len(want) || cfg.TrustedProxies[0] != want[0] || cfg.TrustedProxies[1] != want[1] {
		t.Errorf("TrustedProxies = %v, want %v", cfg.TrustedProxies, want)
	}

	if _, err := Load(env(with(validEnv(), map[string]string{"TRUSTED_PROXIES": "not-a-cidr"}))); err == nil ||
		!strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Errorf("Load() error = %v, want TRUSTED_PROXIES refused", err)
	}
}
