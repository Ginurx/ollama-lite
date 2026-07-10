# ollama-lite

A tiny, cloud-only [Ollama](https://ollama.com)-compatible server.

> **Disclaimer:** ollama-lite is an unofficial, independent project. It is not
> affiliated with, endorsed by, or sponsored by Ollama or its maintainers.
> "Ollama" and any related names are trademarks of their respective owners; they
> are used here only to describe compatibility. Use of the Ollama cloud service
> is subject to Ollama's own terms.

The official Ollama installer is >1GB because it bundles the whole local
inference stack (llama.cpp, GPU discovery, model storage). If you only want to
run **cloud models** — which execute on `ollama.com`, not on your machine — none
of that is needed.

`ollama-lite` starts an Ollama-compatible server on `127.0.0.1:11434` and
**signs and forwards every request to `ollama.com`** using the same
`~/.ollama/id_ed25519` key the official Ollama uses. Anything that already speaks
to Ollama — Open WebUI, editor plugins, the OpenAI-compatible `/v1/*` clients —
works unchanged and runs cloud models. The whole thing is a single ~11MB binary
with no cgo.

## Install

```sh
go build -o ollama-lite .
```

Requires Go 1.24+ (the only dependency is `golang.org/x/crypto`).

## Usage

```sh
# 1. Connect this machine to your ollama.com account (once).
#    Reuses your existing Ollama key/signin if you already have one.
ollama-lite signin

# 2. Start the server.
ollama-lite serve

# 3. Use it like any Ollama server.
curl http://127.0.0.1:11434/api/chat -d '{
  "model": "glm-5.2",
  "messages": [{"role": "user", "content": "hello"}]
}'
```

Other commands:

```sh
ollama-lite whoami     # show the signed-in account
ollama-lite signout    # disconnect this machine from ollama.com
ollama-lite version
```

> **Note:** ollama-lite binds `:11434`, the same port as the official Ollama.
> Stop a running `ollama serve` first, or listen elsewhere with the `--host`
> flag (`ollama-lite serve --host 127.0.0.1:11435`) or the `OLLAMA_HOST`
> environment variable. The flag takes precedence and accepts the same forms:
> `HOST:PORT`, `:PORT`, `HOST`, or `scheme://host:port`.

## Launch an app

`ollama-lite launch <app>` starts a supported AI coding tool **pre-wired to use
the ollama-lite server as its backend** — it injects the right environment
variables and/or writes the app's config to point at your server, then runs the
app.

```sh
# Start the server, then launch an app against it.
ollama-lite serve &
ollama-lite launch claude
ollama-lite launch claude --model gpt-oss:120b
ollama-lite launch codex -- --sandbox workspace-write   # args after -- go to the app
```

- `--model MODEL` — which model the app should use (default: the first advertised
  model). Both `glm-5.2` and `glm-5.2:cloud` work — the server normalizes the suffix.
- `--host HOST` — the ollama-lite address the app should connect to (overrides
  `OLLAMA_HOST`); an unspecified bind like `0.0.0.0` is rewritten to loopback.
- Anything after `--` is passed to the app unchanged.

| App | Name | How it's configured |
| --- | --- | --- |
| Claude Code | `claude` | env (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN=ollama`) |
| Codex | `codex` | own `--profile` at `~/.codex/ollama-launch.config.toml` + `~/.codex/model.json` |
| Copilot CLI | `copilot` (`copilot-cli`) | env (`COPILOT_PROVIDER_BASE_URL`) |
| OpenCode | `opencode` | inline config via `OPENCODE_CONFIG_CONTENT` |
| Qwen Code | `qwen` | `~/.qwen/settings.json` + env (`OPENAI_BASE_URL`) |
| Droid | `droid` | `~/.factory/settings.json` |
| Cline | `cline` | `~/.cline/data/...` |
| Pool | `pool` | env (`POOLSIDE_STANDALONE_BASE_URL`) — not supported on Windows |

The app must already be installed; if it isn't, launch prints the official
install command/URL and exits — it never runs an installer. Apps that write a
config file back up any existing file to a sibling `.ollama-lite.bak` and preserve
your other (non-Ollama) settings. Codex uses its own `--profile`, so your
`~/.codex/config.toml` is left untouched.

Run `ollama-lite launch --help` for the current app list.

## How it works

- **Liveness / listing** (`/`, `/api/version`, `/api/tags`, `/v1/models`) are
  answered locally.
- **Everything else** (`/api/chat`, `/api/generate`, `/api/embed`,
  `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`, `/v1/responses`,
  `/v1/messages`, `/api/me`, …) is signed with your Ed25519 key and
  reverse-proxied — streaming — to `ollama.com`.
- A `:cloud` / `-cloud` suffix on the request's `model` is stripped before
  forwarding, so both `glm-5.2` and `glm-5.2:cloud` work.

The Authorization header is byte-for-byte identical to Ollama's: the challenge
`"<METHOD>,<request-uri-with-ts>"` is signed with `~/.ollama/id_ed25519` and sent
as `Authorization: <public-key-blob>:<base64 signature>`.

## Configuration

Shared with the official Ollama:

| Variable | Purpose | Default |
| --- | --- | --- |
| `OLLAMA_HOST` | Address to listen on | `127.0.0.1:11434` |
| `OLLAMA_ORIGINS` | Extra allowed CORS origins (comma-separated) | localhost/app/tauri/vscode defaults |
| `OLLAMA_CLOUD_BASE_URL` | Upstream cloud endpoint | `https://ollama.com` |

ollama-lite specific:

| | Purpose |
| --- | --- |
| `--host HOST:PORT` (serve flag) | Address to listen on; overrides `OLLAMA_HOST` |
| `--models a,b,c` (serve flag) | Models to advertise on `/api/tags` and `/v1/models` |
| `~/.ollama-lite/models.json` | JSON array of model names (used when `--models` is unset) |
| `OLLAMA_LITE_OLLAMA_VERSION` | Version string reported on `/api/version` (default tracks a real Ollama release) |

If neither the flag nor the file is set, the advertised list is seeded from any
models in your `~/.ollama/config.json` integrations plus a small built-in
default set. Model listing only affects discovery (UI dropdowns) — you can always
request any cloud model by exact name.

## What it does *not* do

No local inference, no `ollama pull`/model storage, no GPU, no Modelfiles. It is
purely a signing proxy to Ollama's cloud.
