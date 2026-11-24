## What this changes and why

<!-- Title: Area: what changed (#issue) — e.g. "CI: gate e2e by paths (#12)".
     Keep the description short: facts, no water. -->

<!-- One change per pull request. Say what it does and what problem it solves. -->

Fixes #

<!-- Anything larger than a small fix needs an issue we've agreed on first (see CONTRIBUTING.md). -->

## How you tested it

<!-- On a real machine: the commands you ran and their output, before and after the change.
     For the Studio, a screenshot of before and after. -->

```text

```

Tested on: <!-- CI / local, Go version -->

## How you found it

- [ ] I use DomainCraft and ran into this myself
- [ ] I found it by reading the code or comparing with other compilers
- [ ] Someone else reported it (link the issue above)

AI tools used to write this change: <!-- yes / no; if yes, which parts and why -->

## Checklist

- [ ] Commits are signed and verified ([GitHub shows "Verified"](https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification))
- [ ] The author of this PR is a person; an AI agent is not the contributor (see [CONTRIBUTING.md → Pull requests](CONTRIBUTING.md#pull-requests))
- [ ] `make fmt`, `make lint` and `make test` pass
- [ ] Deterministic output: two runs on the same `domain.yaml` produce identical bytes
- [ ] If this fixes a bug, a regression check exists and is named in `compliance-suite/regressions.tsv`
