// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rootCertPEM is a self signed CA certificate, generated for the test.
func rootCertPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "wishlist test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestLoadReadsTheRootCertificatePath(t *testing.T) {
	t.Parallel()

	path := writeFile(t, rootCertPEM(t))
	for _, mode := range []string{"verify-ca", "verify-full", "require"} {
		cfg, err := Load(env(with(validEnv(), map[string]string{"DB_SSLMODE": mode, "DB_SSLROOTCERT": path})))
		if err != nil {
			t.Fatalf("%s: Load() error = %v", mode, err)
		}
		if cfg.Database.SSLRootCert != path {
			t.Errorf("%s: SSLRootCert = %q, want %q", mode, cfg.Database.SSLRootCert, path)
		}
	}
}

func TestLoadRefusesAnInconsistentRootCertificate(t *testing.T) {
	t.Parallel()

	good := writeFile(t, rootCertPEM(t))
	cases := []struct {
		name    string
		changes map[string]string
		want    string
	}{
		{"verify-full without a root", map[string]string{"DB_SSLMODE": "verify-full"}, "required when DB_SSLMODE is verify-full"},
		{"verify-ca without a root", map[string]string{"DB_SSLMODE": "verify-ca"}, "required when DB_SSLMODE is verify-ca"},
		{"disable with a root", map[string]string{"DB_SSLMODE": "disable", "DB_SSLROOTCERT": good}, "refused when DB_SSLMODE is disable"},
		{"allow with a root", map[string]string{"DB_SSLMODE": "allow", "DB_SSLROOTCERT": good}, "refused when DB_SSLMODE is allow"},
		{"prefer with a root", map[string]string{"DB_SSLMODE": "prefer", "DB_SSLROOTCERT": good}, "refused when DB_SSLMODE is prefer"},
		{"missing file", map[string]string{"DB_SSLMODE": "verify-full", "DB_SSLROOTCERT": filepath.Join(t.TempDir(), "none.crt")}, "cannot read"},
		{"no certificate in the file", map[string]string{"DB_SSLMODE": "verify-full", "DB_SSLROOTCERT": writeFile(t, "not a certificate\n")}, "holds no PEM certificate"},
		{"require with a missing file", map[string]string{"DB_SSLMODE": "require", "DB_SSLROOTCERT": filepath.Join(t.TempDir(), "none.crt")}, "cannot read"},
		{"require with no certificate in the file", map[string]string{"DB_SSLMODE": "require", "DB_SSLROOTCERT": writeFile(t, "not a certificate\n")}, "holds no PEM certificate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(env(with(validEnv(), tc.changes)))
			if err == nil {
				t.Fatal("Load() error = nil, want an error")
			}
			for _, want := range []string{"DB_SSLROOTCERT", tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// The root certificate problem comes back together with the others.
func TestLoadAggregatesTheRootCertificateProblemWithTheRest(t *testing.T) {
	t.Parallel()

	_, err := Load(env(map[string]string{"DB_SSLMODE": "verify-full", "DB_PORT": "0"}))
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	for _, want := range []string{"DB_SSLROOTCERT", "DB_PORT", "DB_HOST"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregated error does not mention %s:\n%v", want, err)
		}
	}
}

// A mode that does not exist is reported by the mode check alone.
func TestAnUnknownModeAddsNoRootCertificateError(t *testing.T) {
	t.Parallel()

	_, err := Load(env(with(validEnv(), map[string]string{"DB_SSLMODE": "maybe"})))
	if err == nil || strings.Contains(err.Error(), "DB_SSLROOTCERT") {
		t.Errorf("error = %v, want one about DB_SSLMODE only", err)
	}
}
