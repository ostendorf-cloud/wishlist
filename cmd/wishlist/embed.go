// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import "embed"

// distFS is the built SPA, written by "task web:build" (vite build, with
// outDir pointed at this directory). embed resolves relative to this file,
// which is why the directory sits under cmd/ rather than internal/.
//
//go:embed dist
var distFS embed.FS
