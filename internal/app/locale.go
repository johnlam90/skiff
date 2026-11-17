// =============================================================================
// File: internal/app/locale.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-11-16
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// locale.go keeps a wrong locale from turning the whole UI into question
// marks.
//
// tcell picks its output encoding from LC_ALL / LC_CTYPE / LANG, and a
// "C" or "POSIX" locale means US-ASCII to it: every glyph outside ASCII
// — the ≡ button, tab ×, box drawing, the scrollbar — is written as "?"
// or a look-alike ("#", "o"). Skiff's habitat is SSH, which is exactly
// where the locale arrives wrong: the client forwards its own LC_*
// (SendEnv), a multiplexer or a wrapper exports LC_ALL=C for stable
// tool output, and the terminal at the far end of the wire is a UTF-8
// terminal all the same. "C" is what a session gets when nobody chose,
// not a statement about the display, so it is not honoured as one.

package app

import "os"

// asciiOptOutEnv is the escape hatch for a terminal that really cannot
// show UTF-8: set it (to anything) and a C/POSIX locale is honoured.
const asciiOptOutEnv = "SKIFF_ASCII"

// localeVars are the variables tcell reads, in POSIX precedence order.
var localeVars = []string{"LC_ALL", "LC_CTYPE", "LANG"}

// effectiveLocale returns the first locale variable that is set, and its
// value — the one tcell will decide from. Both are "" when none is set
// (which tcell already treats as UTF-8).
func effectiveLocale() (name, value string) {
	for _, v := range localeVars {
		if val := os.Getenv(v); val != "" {
			return v, val
		}
	}
	return "", ""
}

// localeForcesASCII reports whether the environment would make tcell
// fall back to US-ASCII for no better reason than a C/POSIX locale. An
// explicit non-UTF-8 charset (en_US.ISO8859-1) is a real declaration
// about the terminal and is left alone, as is anything under the
// SKIFF_ASCII opt-out.
func localeForcesASCII() bool {
	if os.Getenv(asciiOptOutEnv) != "" {
		return false
	}
	_, val := effectiveLocale()
	return val == "C" || val == "POSIX"
}

// withUTF8Locale runs init — the screen's Init, which is the one moment
// tcell reads the locale — with a UTF-8 locale in place when the
// environment would otherwise force ASCII, then puts the variable back.
// The restore matters: skiff's children (git, formatters, custom
// actions) must inherit the locale the user's session actually has, and
// a tool that was promised LC_ALL=C output must keep getting it.
func withUTF8Locale(init func() error) error {
	if !localeForcesASCII() {
		return init()
	}
	name, old := effectiveLocale()
	os.Setenv(name, "en_US.UTF-8")
	defer os.Setenv(name, old)
	return init()
}
