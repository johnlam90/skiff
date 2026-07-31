---
title: "FAQ"
metaTitle: "Skiff FAQ — Vim, Plugins, LSP, License"
metaDescription: "Why no vim mode, no tree-sitter, no config file, no Ctrl shortcuts. Plus license, contributing, LSP, multi-cursor, plugins, and Windows arm64."
summary: "Quick answers to the most-asked questions."
weight: 130
---

## Why no vim mode?

Because the people who want vim already have vim, and the people who want Skiff picked it specifically to *not* deal with modal editing. Adding a vim mode would compromise both audiences. If you want modal editing, run Neovim — it's better at being Neovim than Skiff ever will be.

## Why not tree-sitter?

Tree-sitter requires CGO, per-language grammar packaging, and platform-specific build steps. Skiff ships as a single static Go binary on purpose — drop it on a server, run it. Chroma gives you syntax highlighting in pure Go with zero setup. Highlighting is all Skiff needs; structural editing is a feature for a different product.

## Why no config file?

A config file is a tax. Every option you expose is one more thing the user has to research, decide on, and maintain. Skiff picks defaults. The two things that *do* vary per-user (custom actions, format-on-save commands) live in tiny JSON files with three or four fields. That's it.

## Why no `Ctrl+` shortcuts?

Three reasons. (1) `Ctrl+S` is XOFF — a real flow-control signal that freezes the terminal. (2) tmux and Zellij both reserve `Ctrl+B` (or whatever you've configured as their prefix) for their own commands. (3) Terminal emulators reserve combinations like `Ctrl+Shift+T` for tab management. The intersection of "available `Ctrl+` keys" and "memorable shortcuts" is roughly empty. Esc-leader sidesteps the whole mess.

## Will you support Windows arm64?

Probably eventually, when GoReleaser's Windows arm64 support stabilizes and there's user demand. Today, Windows ships amd64 only. The Linux arm64 build covers WSL on arm64 Windows machines if you need it.

## Does it work in WSL?

Yes. WSL is just Linux from Skiff's perspective. Install the Linux binary with the curl one-liner, run it inside Windows Terminal, and OSC 52 will copy text to the Windows clipboard.

## How do I contribute?

Open a PR against [johnlam90/skiff](https://github.com/johnlam90/skiff). The contribution rules are in the repo's `CLAUDE.md`: every source file gets a corresponding `_test.go` file, every public and private function gets a doc comment, every new file gets the standard header. CI runs `go test ./...` on every push and PR — broken tests block merges.

## What's the license?

MIT. See the `LICENSE` file in the repo. Use it commercially, fork it, ship it, do whatever. Attribution is appreciated but not required.

## Does it have an LSP?

No. Skiff is intentionally not an IDE. Run a separate tmux pane with `gopls`, `tsserver`, or whatever your language's tooling is, and use Skiff for the "open this file, fix the line, save" loop.

## Does it support multi-cursor?

No. Multi-cursor is an IDE feature; the Skiff value prop is "the editor your fingers don't have to learn." Adding multi-cursor would mean adding the keybindings to manage it, which means adding a config file, which means becoming VS Code. Use a real IDE for refactors.

## Will there ever be plugins?

No. Plugins are how editors stop being editors and start being platforms. Skiff is opinionated — that's the product. Custom actions cover the "shell out to a thing" extension point, which is what 90% of plugin requests boil down to.

## Why "Skiff"?

A skiff is a small, fast boat you drop in the water to row over to another vessel — which is what this editor is: one light binary you drop onto a remote box to do quick work and row back. Skiff is a fork of [SpiceEdit](https://github.com/cloudmanic/spice-edit) by Spicer Matthews, extended with a deeper git integration.

## Where do I report bugs?

[GitHub Issues](https://github.com/johnlam90/skiff/issues). Include your terminal emulator, OS, Skiff version (`skiff --version`), and a recipe to reproduce. Bonus points for a screenshot.
