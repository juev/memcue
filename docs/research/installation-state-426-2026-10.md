# Installation state is not a fact: the rule at extraction and in vet, October 2026

Status: **measured on a copy of a live base, 2026-10-26.** One change for
[#426](https://github.com/juev/memcue/issues/426): the extraction prompt, the review prompt and the vet rule now say
that the state of an installation — a version, the path of a binary or a wrapper, the variables a wrapper exports, a
config default, the size of a base — is read with a command and is not stored as a fact. Names of private hosts and
repositories are replaced by placeholders; the numbers are the ones the run gave.

## Why a rule and not a correction

A stored copy of such state is wrong twice: it is stale with the next release, and it wins the ranking on the very
questions a command answers — "which version is installed", "where is the binary", "how many facts are in the base".
Correcting one fact does not help: the extractor writes the next one after the following session. So the rule sits at
both ends of the queue: the extraction prompt does not write the fact, and vet drops it when the model wrote one anyway.

## What the rule matches

Four signals, each firing only together with a word near it, so a fact that merely mentions a version or a path is
left alone:

| signal | fires with |
|---|---|
| `version` | a release number `1.2.3` / `v0.40.1` within 60 characters of an installation word (версия, установлен, бинарь, обёртка, релиз, тег, сборка, and their English forms) |
| `path` | a path through `/bin/` or `/plugins/` within 60 characters of a place word (лежит, находится, хранится, путь) or of an installation word |
| `config default` | a dotted key such as `memory.memcue.search_facts` near «по умолчанию» / `by default` / `default` |
| `size` | «живых фактов», «фактов в базе», «размер базы», `live facts`, «занимает 12 МБ» |

Three filters keep it from over-reaching: an address such as `10.0.0.5` is not a version, a file name such as
`config.yaml` is not a config key, and a host name such as `git.example.org` is not one either. The rule applies only
to candidates whose kind is `environment`, `other` or empty: a rule, a decision, a pitfall or a procedure that names a
version is knowledge about that version, not a copy of it.

## What it decides on the base

On a copy of the live base, **13 of 386 approved facts** match the rule: 8 path, 3 version, 1 config default, 1 size.
Read by hand, most are exactly what the issue names — the wrapper and the binary of the installation, the installed
release of the agent, the plugins directory of the previous provider, the size of an old database — and a few are
borderline: a service's own binary path under `/usr/local/bin` is state a command reads, and the rule drops it too.
Two candidate matches were false positives before the filters and are left alone now: a host name read as a config
key, and a file name read as one.

Of the 4 stored candidates, 1 is dropped by the rule — a fact that spells out the installed release together with the
binary and the plugin path. The other three are rules and are untouched.

The existing 13 are **retired in a separate pass** after this release is installed, as the issue asks: the pass runs
with the released binary, so the rule that retires them is the rule that shipped.

## Tests

`go vet ./...` clean, `go test ./...` all packages ok. `TestRulesDropInstallationState` covers a wrapper fact with a
version, a binary path, a config default and a base size (each dropped with its own reason), and keeps a pitfall with
a version, a rule about a config key, a service fact with no signal, an address, a file name and a default branch
named after a host. `TestRulesMirrorBeforeInstallationState` pins the order: a mirror of the built-in notes is decided
by the mirror rule first.

## What is not measured

The extraction prompt is prose, and its effect can only be seen in the facts of the next sessions; nothing here
predicts how many of those 13 the prompt alone would have prevented. A second base, a second labeller, and the
review model's own behaviour are not measured.
