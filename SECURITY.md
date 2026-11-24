# Security

DomainCraft generates code and can run database-migration commands on your
behalf, so security reports matter.

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private vulnerability
reporting on this repository (Security → Report a vulnerability).

Include what you found, how to reproduce it, and the DomainCraft version
(`domaincraft --version`). You should get a reply within a few days, and a
fix or a plan once the issue is confirmed. Reporters are credited in the
release notes unless they prefer not to be.

## Supported versions

Security fixes go into the latest release. Update before reporting.

## In scope

- Generated code that runs arbitrary commands (migrations, post-generate
  hooks).
- Bridge resolution and cloning (the bridge cache in `~/.domaincraft`).
- `generate` / `--prune` / `--migrate` flows that read, rename or delete
  files on disk.
- Package-version resolution from registries (NuGet and others).
