# Release notes

Where DomainCraft is going, and what has shipped. Versions are tied to tags
(`v*`), not dates; release builds are produced by the release workflow and
published on the [Releases](https://github.com/DomainCraft/DomainCraft/releases)
page with checksums.

## The current release

**Latest:** v0.7.0 ([notes](docs/releases/v0.7.0.md)) — see the
[Releases](https://github.com/DomainCraft/DomainCraft/releases) page for the
binaries and checksums. Per-version notes live in [`docs/releases/`](docs/releases/).

## How releases are made

1. Tag the commit with `vX.Y.Z`; the release workflow builds the cross-platform
   binaries and generates checksums.
2. Write the release notes in the same voice as the code: short declarative
   sentences, concrete, no marketing fluff. Structure:

   - **New** — what appeared (bullets, each starting with a verb or a bold area word).
   - **Fixed and improved** — each bullet names the area first (Core, Bridges,
     Studio, Docs…), then what changed.
   - **Security** — when there are security fixes, they go first, with a clear
     "update now" note.

3. Every fix references its issue or pull request number.

## Template for a release note

```markdown
## Security
- **Something was fixed.** One line on what and for whom.

## New
- **Area: what you can now do.**

## Fixed and improved
- **Area: what stopped being broken.** (#123)
```
