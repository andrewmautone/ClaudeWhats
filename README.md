# ClaudeWhats

A local, searchable archive of your WhatsApp account, built as a **Claude Code plugin**. A small Go daemon keeps every message in a SQLite database on your machine, transcribes voice notes and describes images with Gemini as they arrive, and exposes a CLI that Claude uses to read, search, summarize, export and send messages — plus a persistent memory so Claude remembers who is who across sessions.

Everything stays on your computer. Nothing is uploaded except the media sent to Gemini for transcription.

## What you can ask Claude

- "What did Maria say this morning?" / "Read the *Family* group"
- "Summarize my WhatsApp from the last 24 hours"
- "Find where we talked about the invoice"
- "Give me the raw messages from the FF group, 30 to 10 days ago, saved to a file"
- "Pull older history from that chat"
- "Reply to João: *I'll be there at 6*"
- "The `...@lid` in the group is Maria — link them"

## Installation

Requirements: [Claude Code](https://claude.com/claude-code), a WhatsApp account on your phone, and a free Gemini API key (see below). No Go toolchain needed — the release binary is downloaded automatically.

### 1. Install the plugin

In Claude Code:

```
/plugin marketplace add andrewmautone/ClaudeWhats
/plugin install claudewhats@claudewhats
```

This installs the skill and puts `claudewhats` on Claude's PATH through small shims in `bin/`. On first use the shim downloads the matching release binary (`claudewhats_<os>_<arch>[.exe]`) into `~/.claudewhats/bin/`. Windows, macOS and Linux (amd64/arm64) are supported.

### 2. Add your Gemini API key

Get a free key at https://aistudio.google.com/apikey — the free tier is enough for personal use.

**Automatic (talk to Claude):** paste the key in chat — *"here's my Gemini key: AIza..."* — and Claude stores it with `claudewhats config set gemini_api_key <key>`. Ask `claudewhats config show` any time to confirm it's set (the key itself is always masked, never echoed back).

**Manual:** create `~/.claudewhats/config.yaml`:

```yaml
gemini_api_key: AIza...        # required for transcription and summaries
# gemini_model: gemini-2.5-flash   # default
# port: 7411                       # daemon loopback port
# idle_timeout: 30m                # background daemon exits after this idle time
```

Or set the environment variable `GEMINI_API_KEY` (this always overrides the file).

**What Gemini is used for** (and nothing else):

| Task | When | Model |
|---|---|---|
| Transcribe voice notes / audio | as each audio arrives (background queue, with retries) | `gemini-2.5-flash` |
| Describe images and read text in them | as each image arrives | `gemini-2.5-flash` |
| Summarize conversations | only when you ask for a summary (`summary`) | `gemini-2.5-flash` |

Text messages never go to Gemini. Without a key everything still works — audio/images simply stay `pending` until you add one, and `summary` reports the missing key. The key is sent as a request header, never in URLs or logs.

### 3. Pair your WhatsApp

**Automatic (talk to Claude):** just ask **"pair my WhatsApp"**. Claude runs `claudewhats pair`, which starts the daemon in the background and opens a page in your browser showing the QR code (it refreshes itself every 2 s and turns into "✅ Connected" when done). On your phone: WhatsApp → **Linked devices** → **Link a device** → scan.

**Manual:**

```
claudewhats pair --wait      # opens the QR page and waits until paired
claudewhats serve            # prints the QR in the terminal instead (Ctrl+C after "conectado")
```

After pairing, WhatsApp sends the recent history of your chats and your address book; from then on every new message is archived as it arrives.

### 4. Use it

Ask Claude anything from the list above. Under the hood the skill runs:

```
claudewhats chats [--since 7d]                     # conversations with activity
claudewhats read "<chat>" [--since 24h] [--until 2026-09-21] [--limit 0] [--out]
claudewhats search "<words>" [--chat X] [--since 7d]
claudewhats summary [--since 24h] [--chat X]        # Gemini summary per chat + overall
claudewhats sync "<chat>" --count 100                # request older history from the phone
claudewhats send "<chat>" "<text>" --yes
claudewhats contact add|list|link|rename
claudewhats memory wake|note|nap|recall|zoom|forget
claudewhats config show|set|path
claudewhats status | stop | version
```

`<chat>` accepts a saved contact name, a group name, a phone number or a JID; ambiguous names return the candidates. `read --out` saves the raw messages to `~/.claudewhats/exports/` (use `--out=<path>` for a specific file). All commands accept `--json`.

## How it works

- **Daemon, no windows.** Any command that needs WhatsApp (`send`, `sync`, pairing) starts a detached daemon (no console window) that stays up while you use it and exits after 30 minutes idle. Reads never touch WhatsApp — they query SQLite directly. WhatsApp delivers the offline backlog on the next connection, so nothing is lost while the daemon is down. Log: `~/.claudewhats/daemon.log`; `claudewhats stop` ends it.
- **Identity.** WhatsApp uses two IDs for the same person (phone number and a "LID" in groups). ClaudeWhats links them automatically and never shows a raw LID: you see the saved contact name, else the WhatsApp profile name, else the number.
- **Memory.** `claudewhats memory` is a Go port of OptMem's [`memo`](https://github.com/VictorTaelin/OptMem), byte-compatible with its on-disk format, stored in `~/.claudewhats/memory/`. Claude writes one-line facts ("the FF group is *FF | Team*", "Maria prefers 6 pm") and reads them before every request; `read`/`summary --chat` also show what is already known about that chat. The same folder can be read by the original Python tool: `MEMORY_DIR=~/.claudewhats/memory memo wake`.
- **Safety.** The daemon marks nothing as read and reports presence as *unavailable*, so notifications on your phone keep working. It never re-pairs by itself. `send` requires an explicit request and confirmation.
- **Storage.** `~/.claudewhats/data.db` (messages, contacts, WhatsApp session), `media/` (downloaded audio/images), `memory/`, `exports/`.

Built on [whatsmeow](https://github.com/tulir/whatsmeow) (unofficial WhatsApp Web API — use a personal account and be mindful of WhatsApp's terms).

## Updating

- Plugin/skill: `/plugin update claudewhats`.
- Binary: delete `~/.claudewhats/bin/claudewhats*` to fetch the latest release on the next call, or pin a version with `CLAUDEWHATS_VERSION=vX.Y.Z`.

## Installing without the plugin

- Download `claudewhats_<os>_<arch>[.exe]` (and `checksums.txt`) from the [latest release](https://github.com/andrewmautone/ClaudeWhats/releases/latest) and put it on your PATH; or `go install github.com/andrewmautone/claudewhats/cmd/claudewhats@latest`.
- Copy `skills/claudewhats/` to `~/.claude/skills/claudewhats/`.

## Development

```
go test ./...            # 7 packages, no cgo
go build ./cmd/claudewhats
```

Releases are built by GoReleaser on every `v*` tag. Design notes live in `docs/superpowers/`.

## Known Windows caveats

- If the process that starts `claudewhats` runs inside a Job Object with kill-on-close, the detached daemon may die with it (`claudewhats status` shows no daemon right after a command that should have started one). Fix: `CREATE_BREAKAWAY_FROM_JOB` in `internal/daemon/spawn_windows.go`.
- A home directory containing `#`, `?` or `%` breaks the SQLite `file:` URI. Move the data dir with `CLAUDEWHATS_HOME`.
