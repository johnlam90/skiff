// =============================================================================
// File: internal/version/version.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package version exposes Skiff's release version. Keep this file
// tiny — it's the single source of truth that release CI will bump,
// and a one-line diff is trivial to review.
package version

// Version is the Skiff release version, displayed in the menu footer.
// Bump this constant on each release (or let release automation do it).
// 0.1.0 is Skiff's first release as its own project.
const Version = "0.2.7"
