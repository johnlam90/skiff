# Security policy

## Reporting a vulnerability

Please report security vulnerabilities privately, not through a public
GitHub issue. Use GitHub's private vulnerability reporting: go to the
repo's **Security** tab and click **Report a vulnerability**.

## Supported versions

Only the latest release is supported. Skiff auto-releases on every merge
to `main`, so the latest GitHub release is always the version to check
a fix against.

## Scope

This includes Skiff's trust-prompt surfaces — the formatter's
project/defaults trust gate and custom-action prompts — since bypassing
either of those is a security issue, not just a bug.
