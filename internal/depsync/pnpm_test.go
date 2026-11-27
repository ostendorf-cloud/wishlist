// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package depsync

import (
	"os"
	"regexp"
	"testing"
)

var (
	// dockerfilePnpm is the version argument Renovate updates. dockerfileCorepack is the
	// one install line, which must take that argument and no literal.
	dockerfilePnpm     = regexp.MustCompile(`(?m)^ARG PNPM_VERSION=(\S+)$`)
	dockerfileCorepack = regexp.MustCompile(`corepack prepare (\S+) --activate`)
)

// TestDockerfilePnpmMatchesMise fails when the Dockerfile's frontend stage installs a
// different pnpm than mise.toml pins. The lockfile the image reads with --frozen-lockfile
// has to come from the same pnpm a local install uses.
func TestDockerfilePnpmMatchesMise(t *testing.T) {
	t.Parallel()

	docker, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}

	d := dockerfilePnpm.FindStringSubmatch(string(docker))
	if d == nil {
		t.Fatal("Dockerfile has no 'ARG PNPM_VERSION=<version>' line")
	}
	installs := dockerfileCorepack.FindAllStringSubmatch(string(docker), -1)
	if len(installs) != 1 || installs[0][1] != `"pnpm@${PNPM_VERSION}"` {
		t.Fatalf(`Dockerfile must install pnpm once, as corepack prepare "pnpm@${PNPM_VERSION}" --activate, got %q`, installs)
	}
	if pin := misePnpm(t); d[1] != pin {
		t.Fatalf("Dockerfile installs pnpm %s but mise.toml pins pnpm %s; they must match", d[1], pin)
	}
}
