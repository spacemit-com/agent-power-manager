<div align="center">

# Agent Power Manager

**A command-line tool that manages Skill, Plugin, MCP and Expert powers for AI agents**

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

[English](README.md) | [简体中文](README_zh.md)

</div>

---

## Overview

APM (Agent Power Manager) is a command-line tool written in Go that unifies the discovery,
installation, upgrade, removal and submission of the four kinds of AI agent capability
extension — **Skill**, **Plugin**, **MCP** and **Expert** — under a single command surface.

It synchronises resource metadata from APC (Agent Power Cloud), installs a pinned version into
a target agent, and records every operation as a recoverable transaction. Supported target
agents are `bibit`, `codex`, `claude-code` and `hermes-agent`.

Each power type has its own format, and each agent has its own resource directory, manifest
format and registration mechanism, so maintaining them by hand drifts quickly: versions scatter,
and a failed install leaves a half-written directory behind. APM makes these operations
repeatable, recoverable and inspectable, and keeps a local inventory that is exact to the
installed version.

## How it works

APM is not a standalone tool. It is the **client half of an end-to-end resource pipeline**, and
understanding what it does means seeing how it divides the work with two other systems:

| Component | Role | Where it runs |
| --- | --- | --- |
| **APM** (this repository) | Command-line client: validate, package, upload, search, download, install, upgrade, uninstall, transaction recovery | Developer machines and Bianbu boards |
| **APC** (Agent Power Cloud) | SpacemiT's resource distribution platform: accepts submissions, human review, publish, maintains the catalog, hosts resource files | Server side (`www.spacemit.com` + CDN) |
| [**bibit-agenthub-plugin**](https://github.com/spacemit-com/bibit-agenthub-plugin) | Agent Hub frontend plugin for Bibit: browse-and-install UI inside Bibit, calling APM and consuming its output | Bibit plugin on the board |

```text
1) Entry points: both paths reach the same APM client
┌─────────────────────────────────────────────────────────────────────────┐
│  User CLI runs apm directly: search / list / install / upgrade          │
└─────────────────────────────────────────────────────────────────────────┘
┌─────────────────────────────────────────────────────────────────────────┐
│  bibit-agenthub-plugin: browse/install UI inside Bibit, calls apm       │
└─────────────────────────────────────────────────────────────────────────┘
   │
   |  both merge here
   v
2) APM client (this repo: CLI + lifecycle; the only side mutating local state)
┌─────────────────────────────────────────────────────────────────────────┐
│  validate / deterministic packing: apm validate, apm submit             │
│  catalog sync / version reconciliation: apm search, list, show          │
│  download / size+MD5 verification / versioned cache                     │
│  journal transactions and recovery: install, upgrade, uninstall, fix    │
└─────────────────────────────────────────────────────────────────────────┘
   | upload: HTTPS + Bearer                    ^ fetch: catalog + content (302 -> CDN)
   v                                           |
3) Server and storage (APC + CDN)
┌─────────────────────────────────────────────────────────────────────────┐
│  APC: accept submission -> human review -> publish -> maintain catalog  │
│  CDN: cdn-resource.spacemit.com hosts tar.gz / JSON                     │
└─────────────────────────────────────────────────────────────────────────┘
   | land: APM atomic publish + local inventory update
   v
4) Landing result
┌─────────────────────────────────────────────────────────────────────────┐
│  Target agents: Bibit / Codex / Claude Code / Hermes                    │
│  Local state: .apm/manifests . cache . staging . transactions           │
└─────────────────────────────────────────────────────────────────────────┘
```

### Resource storage

APC is the authoritative store, and it keeps **catalog and files separate**:

- **Catalog**: one JSON document whose top level has the four groups `skills`/`plugins`/`mcps`/`agents`,
  listing each resource's latest version, description, `content_size`, `content_md5`, `download_url`,
  `published_at` and `tags`. It carries **no file bytes**, so it stays small and can be synced often.
- **Resource files**: hosted on the CDN (`cdn-resource.spacemit.com`) and addressed by type and
  version. Skill, Plugin and Expert content is a deterministic `.tar.gz`; MCP is a single
  JCS-canonicalised JSON document.

APM also keeps local state for offline use and transaction recovery:

| Path | Contents |
| --- | --- |
| `.apm/manifests/<agent>.json` | Installed inventory, exact to the version; the sole input to `list --installed` and `upgrade` |
| `.apm/cache/<type>/<name>/<version>` | Versioned content cache, reused on hit instead of re-downloading |
| `.apm/staging/` | Materialisation and validation area before install |
| `.apm/transactions/` | Transaction journals; `apm fix` recovers after a crash |

### Uploading (publishing)

```text
resource dir ──> apm validate --type <t>   full offline validation (no network)
             ──> apm submit --type <t>     deterministic packing + MD5 + HTTP submit
             ──> APC returns submission_id status received
             ──> platform review           review_pending -> approved / rejected
             ──> publish                   publishing -> published (enters catalog)
```

- **Validation comes first.** `validate` is fully offline and rejects format errors before any network
  call; `submit` reuses the same validation.
- **Deterministic packing.** The same input produces a byte-identical archive (stable ordering, fixed
  mtime/uid/gid, no gzip timestamp), so the MD5 is stable, verifiable and reproducible.
- **Credentials only for submission.** Only `submit` and `submission` need `APC_API_KEY`; querying and
  downloading are anonymous.
- **Review is a human step.** APM submits and polls status; publication happens on the APC platform.

### Fetching (installing and upgrading)

```text
apm list / search ──> sync catalog (one snapshot) ──> show installable resources
apm install       ──> resolve version ──> fetch content (CDN, verify size/MD5) ──> validate format
                  ──> write staging ──> journal transaction ──> land into target agent
                  ──> update .apm/manifests
apm upgrade       ──> read local inventory ──> one catalog snapshot ──> plan each
                  ──> confirm ──> execute one by one
```

- **Download on demand.** `list`/`search` fetch only the catalog, never content; only `install` and
  `upgrade` fetch files.
- **Content must prove itself.** After download APM checks the catalog's `content_size` and
  `content_md5`, then runs archive-safety and format validation. Any mismatch fails closed and leaves
  no partial file behind.
- **Cache reuse.** Re-installing the same version hits the cache instead of downloading again.
- **Transactional landing.** Landing is journal-driven, so an interruption can be repaired with
  `apm fix`; re-installing the same version returns `unchanged` and never writes a second copy.

### Boundaries between the three components

**APM is the client only; it does not change the server contract.** APC's HTTP contract is
authoritative in [`APC-API.md`](APC-API.md) and uses `agent`/`agents` on the wire, while APM calls the
category `expert` locally; translation happens only at the HTTP boundary and the catalog group name
([ADR-044](ivory-tower/adr/ADR-044-rename-agent-power-type-to-expert.md)).

**bibit-agenthub-plugin consumes APM rather than replacing it.** The plugin provides a browse-and-install
UI inside Bibit and reads APM's command output and JSON fields such as
`name_zh`/`name_en`/`description_zh`. Because both sides share that JSON contract, **APM and the plugin
must be upgraded together**: a new APM with an old plugin cannot parse the new fields, and vice versa.
See [spacemit-com/bibit-agenthub-plugin](https://github.com/spacemit-com/bibit-agenthub-plugin).

**Target agents are adapted per agent.** The four agents differ in resource directory, manifest format
and registration mechanism; `internal/adapter` isolates those differences. Expert is supported on Bibit
only.

## Capabilities

| Command | Purpose |
| --- | --- |
| `search` | Search installable powers in the cloud by keyword |
| `list` / `list --installed` | Browse the cloud catalog, or inspect what an agent has installed |
| `show` | Show details and available versions of a power |
| `install` | Install a pinned version into a target agent |
| `upgrade` | Upgrade installed powers to the latest versions |
| `uninstall` | Remove a power and clean up the agent-side landing |
| `fix` | Recover an interrupted transaction |
| `validate` / `submit` | Validate resource format offline, and submit it to APC |

Re-installing the same version returns `unchanged`, and re-uninstalling returns `unexecuted`;
neither produces a second copy of anything. `--agent all` runs a command across every agent,
reporting per agent, and stops on the first failure.

## Supported agents and power types

| Item | Values |
| --- | --- |
| Agent (`--agent`) | `bibit`, `codex`, `claude-code`, `hermes-agent`, plus the batch value `all` |
| Power type (`--type`) | `skill`, `plugin`, `mcp`, `expert` |

`expert` is the local type name and corresponds to the `agent`/`agents` fields in the APC API.
The legacy spelling `--type agent` still works but emits a deprecation warning; `--type expert`
is only supported with `bibit`.

An Expert may also carry its own Skill, Plugin and MCP resources inside the same archive, under
`skills/`, `plugins/` and `mcp/` — so a single install delivers an agent together with the
capabilities it needs.

## Quick start

Go 1.26.0 is required (see [`go.mod`](go.mod) for the exact version):

```bash
go mod download
go build -o ./apm ./cmd/apm
./apm --help
```

On a Bianbu development board you can also build a Debian package (installs `/usr/bin/apm`):

```bash
sudo apt-get install debhelper golang-go ca-certificates
dpkg-buildpackage -us -uc -b
sudo apt-get install ../agent-power-manager_<version>_<arch>.deb
```

## Common commands

```bash
# Search and browse
apm search superpowers
apm list
apm list superpowers --all
apm show superpowers=v6.3.0

# Install into a target agent (identical syntax for all four power types)
apm install superpowers=v6.3.0 --type plugin --agent codex
apm install foo-before-summary=v0.0.1 --type skill --agent claude-code
apm install nws=v1.0.0 --type mcp --agent hermes-agent
apm install test-agent=v1.0.0 --type expert --agent bibit

# Inspect the installed inventory
apm list --installed --agent codex --type plugin --json

# Uninstall takes only a name, never a version
apm uninstall superpowers --type plugin --agent codex

# Upgrade
apm upgrade --dry-run
apm upgrade -y
```

`list`, `search`, `show`, `upgrade` and `validate` support `--json`; `submission get/list` use
`--output json`.

When calling `apm upgrade` from a script or CI, pass `-y` explicitly: without it, a
non-interactive terminal or `--json` fails immediately, without taking the lock or syncing the
catalog.

## Configuration

The Remote provider is the default (production endpoint `https://www.spacemit.com`). A
configuration error or remote failure never falls back to Local automatically.

| Environment variable | Default and constraints |
| --- | --- |
| `APM_CLOUD_PROVIDER` | Defaults to `remote`; accepts only `remote` or `local` |
| `APC_BASE_URL` | Defaults to `https://www.spacemit.com`; must be a credential-free, query/fragment-free host-only root URL; non-loopback requires HTTPS |
| `APC_API_KEY` | Required only by `submit` and `submission` |
| `APM_CLOUD_LOCAL_ROOT` | Required when `local` is selected; must be a clean absolute path |
| `APM_HOME` | Parent of the state root, defaulting to the user home; state lives under its `.apm/` |

```bash
# Point at a test environment
APC_BASE_URL=https://developer.bianbu.xyz ./apm list

# Use local fixtures and run offline
APM_CLOUD_PROVIDER=local \
APM_CLOUD_LOCAL_ROOT="$(pwd)/internal/cloud/resources" \
./apm list
```

`APC_BASE_URL` takes the host only; the gateway prefix `/api-server/apc` is appended by the
client.

## Documentation

| Document | Contents |
| --- | --- |
| [BUILD.md](BUILD.md) | Build, test, providers and catalog synchronisation |
| [APC-API.md](APC-API.md) | APC server API contract |
| [docs/power-template/README.md](docs/power-template/README.md) | Format rules and templates for the four power types |
| [CONTEXT.md](CONTEXT.md) | Domain glossary |
| [docs/Changelog.md](docs/Changelog.md) | Changelog |
| [docs/testing/apm-cli-functional-test.md](docs/testing/apm-cli-functional-test.md) | CLI functional testing guide |
| [ivory-tower/adr](ivory-tower/adr) | Architecture decision records (ADR) |

## License

[Apache License 2.0](LICENSE)
