<p align="center">
  <img width="256" height="256" alt="Gemini_Generated_Image_6p04mm6p04mm6p04" src="https://github.com/user-attachments/assets/512cd1d8-93af-40dc-acde-6e7daa339493" />
</p>

<div align="center">

**Codex Credential & Diagnostics CLI**

[![Release](https://img.shields.io/github/v/release/Code9xs/codex-probe?style=flat-square)](../../releases)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-lightgrey?style=flat-square)]()

[English](README.md) · [中文](README_ZH.md)

</div>

`codex-probe` is a single-binary CLI for Codex token login, renewal, quota checks, API smoke tests, credential format conversion, and optional Supabase sync.

## Project Overview

<p align="center">
  <img width="80%" alt="logo" src="https://github.com/user-attachments/assets/9bd3c05b-5274-4bb3-b504-c18a52891181" />
</p>

`codex-probe` turns the raw CLI flags into a small credential workflow you can actually operate day to day.

Features:

- `--login`: start OAuth PKCE login and write a token JSON locally
- `--renew`: refresh one token file or a whole directory in place
- `--status`: read remaining quota windows from existing token files
- `--apitest`: send lightweight requests to verify model availability
- `--convert`: convert credential files to **sub2api** or **CPA** format
- `--serve`: start a **Web Dashboard** with REST API for visual credential management
- `--sync`: encrypt local token files and sync them with Supabase
- `--output`: export `--status` and `--apitest` results as CSV
- `--proxy`: use a fixed proxy or fall back to system proxy detection

For detailed guides, see:

- [docs/advanced.md](docs/advanced.md)
- [docs/supabase.md](docs/supabase.md)

## Install

Pre-built binaries are available in [Releases](../../releases).

| Platform | File |
|---|---|
| Linux x86-64 | `codex-probe-linux-amd64` |
| Linux ARM64 | `codex-probe-linux-arm64` |
| macOS Intel | `codex-probe-darwin-amd64` |
| macOS Apple Silicon | `codex-probe-darwin-arm64` |
| Windows x86-64 | `codex-probe-windows-amd64.exe` |

Build from source:

```bash
git clone https://github.com/Code9xs/codex-probe
cd codex-probe
go build -o codex-probe ./cmd/codex-probe/
```

Cross-platform build (all platforms at once):

```bash
# Build all platforms → ./dist/
./build.sh

# Build current platform only → ./codex-probe
./build.sh current

# Build specific platform
./build.sh darwin-arm64
```

On macOS, remove quarantine before first run if needed:

```bash
xattr -d com.apple.quarantine codex-probe-darwin-*
chmod +x codex-probe-darwin-*
./codex-probe-darwin-*
```

## Quick Start

Copy the example config before first run:

```bash
cp ./config.example.json ./config.json
```

Common commands:

```bash
# Login and save token files
codex-probe --login -o ./tokens/

# Renew one token file in place
codex-probe --renew ./tokens/me.json

# Check quota
codex-probe --status ./tokens/me.json

# Test model availability
codex-probe --apitest ./tokens/

# Encrypt and sync local token files with Supabase
codex-probe --sync

# Start web dashboard
codex-probe --serve --port 8080 ./tokens/
```

## Credential Conversion

The `--convert` command lets you convert between different credential formats:

| Format | Description |
|---|---|
| `sub2api` | Standard [sub2api](https://github.com/AIDotNet/sub2api) import JSON with `accounts[]`, `model_mapping`, `concurrency`, etc. |
| `cpa` | JSONL archive — one complete credential JSON per line |

### Input Types

| Input | How it's read |
|---|---|
| `./tokens/` | Directory — reads all `*.json` credential files |
| `./tokens/me.json` | Single codex-probe credential JSON file |
| `./cpa.txt` | Line-delimited file — each line is a full credential JSON (`.txt` extension) |

### Examples

```bash
# Directory → sub2api JSON
codex-probe --convert --format sub2api ./tokens/

# Single file → sub2api JSON
codex-probe --convert --format sub2api ./tokens/me.json

# CPA line file → sub2api JSON
codex-probe --convert --format sub2api ./cpa.txt

# Directory → CPA line file
codex-probe --convert --format cpa ./tokens/

# Custom output directory
codex-probe --convert --format sub2api --output ./my_output/ ./tokens/

# Interactive — omit --format to choose at runtime
codex-probe --convert ./tokens/
```

### End-to-End Workflow

```
codex-probe --login -o ./tokens/       ← Step 1: obtain credentials
                 ↓
codex-probe --convert --format sub2api ./tokens/  ← Step 2: convert format
                 ↓
           sub2api-5-20260512-215046.json          ← Ready to import
```

## Web Dashboard

The `--serve` command starts a built-in web dashboard with REST API:

```bash
codex-probe --serve ./tokens/             # default port 18152
codex-probe --serve --port 8080 ./tokens/ # custom port
codex-probe --serve                       # empty dashboard
```

Features:

- **Stats overview** — total, valid, and expiring credential counts
- **Drag-drop upload** — supports Codex `.json`, CPA `.txt`, and Sub2API `.json` (auto-detect, saved as codex format)
- **Credential table** — pagination, filtering, select all / select page, batch operations
- **Status detection** — batch check all credentials (marks invalid 401/403 accounts)
- **Format conversion** — convert selected or all credentials to sub2api / CPA
- **Quota checking** — visual 5-hour and weekly quota display per credential
- **OAuth login** — login new accounts directly from the web UI
- **Credential management** — delete, clear, renew individual credentials

### REST API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/health` | Health check |
| `GET` | `/api/keys` | List credentials (with status field) |
| `POST` | `/api/keys/upload` | Upload credential files (codex/CPA/sub2api) |
| `POST` | `/api/keys/batch-check` | Batch check all credentials status |
| `GET` | `/api/keys/{idx}` | Get credential detail |
| `DELETE` | `/api/keys/{idx}` | Delete credential |
| `DELETE` | `/api/keys` | Clear all credentials |
| `GET` | `/api/keys/{idx}/status` | Check quota |
| `POST` | `/api/keys/{idx}/renew` | Refresh credential |
| `POST` | `/api/convert` | Convert format (body: `{format, indices}`) |
| `GET` | `/api/login` | Start OAuth login flow |

## Local Config

By default, `codex-probe` loads `config.json` next to the executable. Start from [config.example.json](config.example.json).

```json
{
  "renew_before_expiry_days": 3,
  "sync_url": "https://<project>.supabase.co",
  "sync_api_key": "<publishable-key>",
  "sync_aes_gcm_key": "<64-char-hex>",
  "sync_dir": "./tokens"
}
```

- `renew_before_expiry_days`: renew when the token is close to expiry
- `sync_url`: Supabase project URL
- `sync_api_key`: Supabase publishable key
- `sync_aes_gcm_key`: local AES-256-GCM key generated with `openssl rand -hex 32`
- `sync_dir`: local directory used by `--sync`

Supabase Free Plan is enough for this workflow.

For Supabase setup, local config details, renew behavior, proxy detection, CSV format, and internals, see:

- [docs/advanced.md](docs/advanced.md)
- [docs/supabase.md](docs/supabase.md)
- [supabase.sql](supabase.sql)

## Build Script

The `build.sh` script cross-compiles for all supported platforms:

```bash
./build.sh              # Build all platforms → ./dist/
./build.sh current      # Build for current platform only
./build.sh clean        # Remove build artifacts
./build.sh darwin-arm64 # Build for a specific platform
VERSION=v1.2.0 ./build.sh  # Set version tag
```

Output artifacts go to `./dist/` with SHA-256 checksums.

## License

MIT

## Community

[![LinuxDO](https://img.shields.io/badge/Community-Linux.do-blue?style=flat-square)](https://linux.do/)

Discuss usage and issues at [linux.do](https://linux.do/).
