// =============================================================================
// File: internal/app/locale_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-11-16
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"testing"
)

// setLocale pins the three locale variables (and the opt-out) for one
// test; "" leaves a variable set-but-empty, which reads as unset.
func setLocale(t *testing.T, lcAll, lcCtype, lang, ascii string) {
	t.Helper()
	t.Setenv("LC_ALL", lcAll)
	t.Setenv("LC_CTYPE", lcCtype)
	t.Setenv("LANG", lang)
	t.Setenv(asciiOptOutEnv, ascii)
}

// TestLocaleForcesASCII pins which environments are overridden: only a
// C/POSIX value in the variable that wins POSIX precedence. The first
// row is the SSH session this was written for — LC_ALL=C beating a
// LANG that says UTF-8 — where every non-ASCII glyph painted as "?".
func TestLocaleForcesASCII(t *testing.T) {
	cases := []struct {
		lcAll, lcCtype, lang, ascii string
		want                        bool
	}{
		{"C", "", "C.UTF-8", "", true},
		{"POSIX", "", "", "", true},
		{"", "C", "en_US.UTF-8", "", true},
		{"", "", "C", "", true},
		{"", "", "", "", false},                // unset: tcell already assumes UTF-8
		{"", "", "C.UTF-8", "", false},         // names UTF-8 itself
		{"en_US.UTF-8", "C", "C", "", false},   // LC_ALL wins
		{"en_US.ISO8859-1", "", "", "", false}, // a real declaration
		{"C", "", "", "1", false},              // explicit opt-out
	}
	for _, c := range cases {
		setLocale(t, c.lcAll, c.lcCtype, c.lang, c.ascii)
		if got := localeForcesASCII(); got != c.want {
			t.Errorf("LC_ALL=%q LC_CTYPE=%q LANG=%q %s=%q: got %v, want %v",
				c.lcAll, c.lcCtype, c.lang, asciiOptOutEnv, c.ascii, got, c.want)
		}
	}
}

// TestWithUTF8Locale_OverridesOnlyDuringInit pins both halves: the
// screen's Init sees a UTF-8 locale, and afterwards the variable is back
// to what the session had, so git and formatters inherit the user's own
// locale rather than one skiff invented.
func TestWithUTF8Locale_OverridesOnlyDuringInit(t *testing.T) {
	setLocale(t, "C", "", "C.UTF-8", "")
	var during string
	if err := withUTF8Locale(func() error {
		during = os.Getenv("LC_ALL")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if during != "en_US.UTF-8" {
		t.Fatalf("LC_ALL during init = %q, want a UTF-8 locale", during)
	}
	if got := os.Getenv("LC_ALL"); got != "C" {
		t.Fatalf("LC_ALL after init = %q, want the session's own \"C\" restored", got)
	}

	setLocale(t, "", "", "en_US.UTF-8", "")
	_ = withUTF8Locale(func() error {
		during = os.Getenv("LANG")
		return nil
	})
	if during != "en_US.UTF-8" || os.Getenv("LC_ALL") != "" {
		t.Fatal("a UTF-8 environment must be left untouched")
	}
}
