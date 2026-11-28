// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/wishlist/internal/config"
)

// authority is a self signed CA with the PEM of its certificate.
type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newAuthority(t *testing.T, name string) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	return authority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// serverCert signs a certificate for the given DNS name and 127.0.0.1.
func (a authority) serverCert(t *testing.T, dnsName string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatalf("create server certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tlsServer speaks just enough of the PostgreSQL protocol to run the TLS
// handshake: it answers the SSLRequest with S and handshakes. It needs no
// database. The result of the handshake, as the server saw it, comes back on
// the channel, one value per connection.
func tlsServer(t *testing.T, cert tls.Certificate) (host string, port int, handshakes <-chan error) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	results := make(chan error, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				request := make([]byte, 8)
				if _, err := conn.Read(request); err != nil {
					results <- err
					return
				}
				if _, err := conn.Write([]byte("S")); err != nil {
					results <- err
					return
				}
				server := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				results <- server.HandshakeContext(context.Background())
			}()
		}
	}()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is %T", ln.Addr())
	}
	return "localhost", addr.Port, results
}

func writeRoot(t *testing.T, pemBytes []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write root certificate: %v", err)
	}
	return path
}

// The client verifies the certificate it is given, with the real pgx client
// against a server that only does the TLS handshake. A wrong CA and a wrong
// host name are refused under verify-full, a wrong host name passes under
// verify-ca, which checks the chain only, and a wrong CA fails there too.
func TestTheClientVerifiesTheServerCertificateAgainstTheRootCertificate(t *testing.T) {
	t.Parallel()

	good := newAuthority(t, "wishlist test CA")
	other := newAuthority(t, "another CA")

	cases := []struct {
		name     string
		mode     string
		root     authority
		certName string
		refused  bool
	}{
		{"verify-full, right CA, right host name", "verify-full", good, "localhost", false},
		{"verify-full, wrong CA", "verify-full", other, "localhost", true},
		{"verify-full, wrong host name", "verify-full", good, "db.example.invalid", true},
		{"verify-ca, right CA, other host name", "verify-ca", good, "db.example.invalid", false},
		{"verify-ca, wrong CA", "verify-ca", other, "localhost", true},
		{"require with a root certificate verifies the chain", "require", other, "localhost", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			host, port, handshakes := tlsServer(t, good.serverCert(t, tc.certName))
			cfg := config.Database{
				Host: host, Port: port, Name: "wishlist", User: "wishlist", Password: dummyPassword,
				SSLMode: tc.mode, SSLRootCert: writeRoot(t, tc.root.pem), MaxConns: 1, ConnectTimeout: 3 * time.Second,
			}
			core, err := Open(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			defer core.Close()

			pingErr := core.pool.Ping(context.Background())
			if pingErr == nil {
				t.Fatal("Ping() error = nil, the server speaks no PostgreSQL")
			}
			verifyFailed := strings.Contains(pingErr.Error(), "x509") || strings.Contains(pingErr.Error(), "certificate")
			if verifyFailed != tc.refused {
				t.Errorf("certificate refused = %v, want %v, error: %v", verifyFailed, tc.refused, pingErr)
			}
			select {
			case hsErr := <-handshakes:
				if (hsErr != nil) != tc.refused {
					t.Errorf("server side handshake error = %v, refused = %v", hsErr, tc.refused)
				}
			case <-time.After(5 * time.Second):
				t.Error("the server saw no handshake")
			}
		})
	}
}

func TestAnUnreadableRootCertificateIsAnErrorOfTheConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Database{
		Host: "db.example.invalid", Port: 5432, Name: "wishlist", User: "wishlist",
		SSLMode: "verify-full", SSLRootCert: filepath.Join(t.TempDir(), "missing.crt"),
	}
	if _, err := poolConfig(cfg); err == nil {
		t.Fatal("poolConfig() error = nil, want the unreadable file refused")
	}
}

func TestDSNCarriesTheRootCertificatePathOnlyWhenSet(t *testing.T) {
	t.Parallel()

	base := config.Database{Host: "db.example.invalid", Port: 5432, Name: "w", User: "u", SSLMode: "verify-full"}
	if got := dsn(base); strings.Contains(got, "sslrootcert") {
		t.Errorf("DSN %s names a root certificate without one set", got)
	}
	base.SSLRootCert = "/etc/ssl/db ca/ca.crt"
	if got := dsn(base); !strings.Contains(got, "sslrootcert=%2Fetc%2Fssl%2Fdb+ca%2Fca.crt") {
		t.Errorf("DSN %s does not carry the escaped path", got)
	}
}
