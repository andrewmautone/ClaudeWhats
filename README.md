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

## Smoke test no Windows — fique de olho
- **Daemon morrendo junto com o CLI pai.** Se quem chamou `claudewhats` roda dentro de um Job Object com kill-on-close (ex.: a ferramenta Bash do Claude Code), o daemon destacado morre quando o pai sai. Sintoma: `claudewhats status` diz que não há daemon logo após um comando que deveria tê-lo acordado. Correção: adicionar `CREATE_BREAKAWAY_FROM_JOB` (0x01000000) em `internal/daemon/spawn_windows.go`, com fallback sem a flag se der `ERROR_ACCESS_DENIED`.
- **Home com `#`, `?` ou `%`.** O caminho do banco entra cru numa URI `file:` do SQLite; esses caracteres quebram a abertura (`unable to open database`). Correção: `filepath.ToSlash` + `url.PathEscape` em `store.Open` e `wa.Open`, ou apontar `db_path` no config para uma pasta sem esses caracteres.
