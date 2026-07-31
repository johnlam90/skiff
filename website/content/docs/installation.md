---
title: "Installation"
metaTitle: "Install Skiff on Mac, Linux, and Windows"
metaDescription: "Install Skiff on macOS via Homebrew, on Linux with a one-line curl script, or on Windows from a GitHub Release. No runtime, no setup steps."
summary: "Install Skiff on macOS, Linux, or Windows."
weight: 10
---

Skiff ships as a single static Go binary. There is no runtime, no node, no language server, no setup. Pick the path that matches your platform.

## macOS (Homebrew)

The Homebrew formula lives in this repo's `Formula/` directory. Tap it by URL — there's no separate `homebrew-tap` repo to remember:

```sh
brew tap johnlam90/skiff https://github.com/johnlam90/skiff
brew install johnlam90/skiff/skiff
```

Both Apple Silicon (`arm64`) and Intel (`amd64`) builds are published on every release. Homebrew picks the right one.

### Updating

```sh
brew update
brew upgrade johnlam90/skiff/skiff
```

### Uninstalling

```sh
brew uninstall johnlam90/skiff/skiff
brew untap johnlam90/skiff
```

## Linux (one-line install script)

The fastest way onto a Linux box — including remote SSH targets and Alpine images:

```sh
curl -fsSL https://raw.githubusercontent.com/johnlam90/skiff/main/install.sh | sh
```

The script detects your OS and architecture (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`), downloads the matching archive from the latest GitHub Release, extracts the `skiff` binary, and drops it into `~/.local/bin` if writable, otherwise `/usr/local/bin`. Re-run the same command to upgrade.

It's plain POSIX `sh` — no bash, no curl-isms. It needs `tar` and one of `curl` or `wget`. Override behavior with environment variables:

```sh
# Pin a specific version (any tag from the Releases page).
curl -fsSL https://raw.githubusercontent.com/johnlam90/skiff/main/install.sh | VERSION=vX.Y.Z sh

# Install to a custom directory.
curl -fsSL https://raw.githubusercontent.com/johnlam90/skiff/main/install.sh | INSTALL_DIR=/opt/bin sh
```

## Linux (manual binary)

If you don't trust pipe-to-sh, grab the archive yourself from the [GitHub Releases](https://github.com/johnlam90/skiff/releases) page:

```sh
# Replace X.Y.Z and ARCH (amd64 or arm64) to taste — pick a tag from the Releases page.
curl -L -O https://github.com/johnlam90/skiff/releases/download/vX.Y.Z/skiff_X.Y.Z_linux_amd64.tar.gz
tar -xzf skiff_X.Y.Z_linux_amd64.tar.gz
mv skiff ~/.local/bin/
```

## Windows

Windows builds (`amd64` only — no arm64 yet) ship as a zipped binary on every GitHub Release.

1. Download `skiff_<version>_windows_amd64.zip` from the [Releases page](https://github.com/johnlam90/skiff/releases).
2. Unzip it.
3. Move `skiff.exe` to a directory on your `PATH` — `C:\Users\<you>\AppData\Local\Programs\skiff\` is a fine choice.
4. Open Windows Terminal and run `skiff`.

There is no installer or Chocolatey package yet. The binary works in Windows Terminal, ConEmu, and WSL.

## Build from source

For the masochists, the contributors, and anyone behind a corporate firewall:

```sh
git clone https://github.com/johnlam90/skiff.git
cd skiff
make install   # builds and installs to $GOPATH/bin
```

Requires Go 1.22 or later. CGO is off by default — the build is fully static.

## Uninstall (manual)

The install script doesn't write anywhere except the binary destination. To remove Skiff, delete the binary:

```sh
rm ~/.local/bin/skiff
# or wherever you installed it
```

Optionally clean up its config and state directories:

```sh
rm -rf ~/.config/skiff ~/.local/state/skiff
```
