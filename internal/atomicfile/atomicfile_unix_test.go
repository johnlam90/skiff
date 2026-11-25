// =============================================================================
// File: internal/atomicfile/atomicfile_unix_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build !windows

// The one atomicfile test that needs a Unix-only call: setting the
// process umask. It lives apart from atomicfile_test.go only because of
// the build tag.

package atomicfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestReplace_NewFileHonoursUmask pins who decides a brand-new file's
// mode: the user's umask, like every other program that creates a file.
// Replace used to chmod a new file to 0644 outright, so under a private
// umask (077) the first save of a .env was world-readable.
//
// The umask is process-wide, so this test must never run in parallel
// with another test in this package.
func TestReplace_NewFileHonoursUmask(t *testing.T) {
	for _, tc := range []struct {
		umask int
		want  os.FileMode
	}{
		{0o077, 0o600},
		{0o022, 0o644},
	} {
		old := syscall.Umask(tc.umask)
		path := filepath.Join(t.TempDir(), "fresh.txt")
		err := Replace(path, []byte("secret"))
		syscall.Umask(old)
		if err != nil {
			t.Fatalf("umask %03o: Replace: %v", tc.umask, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Errorf("umask %03o: new file mode = %03o, want %03o", tc.umask, got, tc.want)
		}
	}
}
