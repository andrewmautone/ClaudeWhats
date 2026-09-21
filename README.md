# ClaudeWhats

Arquivo local do seu WhatsApp (SQLite) com transcrição de áudio/imagem via Gemini e CLI feita para o Claude.

## Setup
1. `go build -o claudewhats.exe ./cmd/claudewhats` e coloque no PATH.
2. `~/.claudewhats/config.yaml`: `gemini_api_key: ...` (ou env `GEMINI_API_KEY`).
3. `claudewhats serve` → escaneie o QR (WhatsApp > Aparelhos conectados). Ctrl+C depois de "conectado".
4. Pronto: `claudewhats chats`, `read`, `search`, `summary`, `send`, `sync`, `contact`.

O daemon sobe sozinho em background quando um comando precisa e encerra após 30 min ocioso (`idle_timeout` no config). Log em `~/.claudewhats/daemon.log`; `claudewhats stop` encerra.

## Skill
Copie `skill/` para `~/.claude/skills/claudewhats/`.
