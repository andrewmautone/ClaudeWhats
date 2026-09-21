# ClaudeWhats

Arquivo local do seu WhatsApp (SQLite) com transcrição de áudio/imagem via Gemini e CLI feita para o Claude.

## Instalação

1. No Claude Code: `/plugin marketplace add andrewmautone/ClaudeWhats` e `/plugin install claudewhats@claudewhats`. Isso instala a skill e coloca `claudewhats` no PATH (via os shims em `bin/`, que baixam o binário da release na primeira execução).
2. `~/.claudewhats/config.yaml`: `gemini_api_key: ...` (ou env `GEMINI_API_KEY`).
3. Pareie: peça ao Claude ("pareia meu WhatsApp") ou rode `claudewhats pair` — o daemon sobe em background e abre sozinho, no navegador, uma página com o QR (`~/.claudewhats/qr.html`, que recarrega o QR a cada 2 s); escaneie em WhatsApp > Aparelhos conectados > Conectar aparelho e rode `claudewhats pair --wait`. Se o navegador não abrir automaticamente, abra `qr.html` manualmente (`--open=false` desliga essa tentativa). Alternativa: `claudewhats serve` no seu terminal imprime o QR direto (Ctrl+C depois de "conectado").

O daemon sobe sozinho em background quando um comando precisa e encerra após 30 min ocioso (`idle_timeout` no config). Log em `~/.claudewhats/daemon.log`; `claudewhats stop` encerra.

## Primeiro uso

Se ainda não pareou, o Claude faz isso pelo chat: roda `claudewhats pair --json`, que já abre a página do QR no seu navegador, e espera com `claudewhats pair --wait --json` (até 3 min). A página mesma cuida de mostrar cada QR novo (expiram em ~20-60 s) e, quando parear, exibe "Pareado". `claudewhats status` mostra `pareado: sim/não`.

Depois do pareamento, peça ao Claude para ver suas conversas — a skill cuida do resto (`claudewhats chats`, `read`, `search`, `summary`, `send`, `sync`, `contact`). `read` aceita `--until` (limite superior do período), `--limit 0` (sem limite) e `--out <arquivo>` para salvar as mensagens cruas num arquivo em vez de imprimir.

## Atualizar

- Skill/plugin: `/plugin update claudewhats`.
- Binário: `rm ~/.claudewhats/bin/claudewhats*` para forçar o download da última release na próxima chamada, ou fixe uma versão com `CLAUDEWHATS_VERSION=vX.Y.Z`.

## Instalar sem plugin

- Baixe o asset certo (`claudewhats_<os>_<arch>[.exe]` + `checksums.txt`) em https://github.com/andrewmautone/ClaudeWhats/releases/latest e coloque no PATH; ou
- `go install github.com/andrewmautone/claudewhats/cmd/claudewhats@latest`.

## Memória
`claudewhats memory` é uma porta em Go do `memo` do OptMem (github.com/VictorTaelin/OptMem), byte-compatível com o formato em disco. Ela mora em `$CLAUDEWHATS_MEMORY_DIR` (padrão `~/.claudewhats/memory`), separada do banco de mensagens, e é usada pela skill para lembrar de pessoas, grupos e combinados entre sessões (`wake`, `note`, `nap`, `recall`, `zoom`, `forget` — veja `skills/claudewhats/SKILL.md`). `read`/`summary --chat X` incluem automaticamente um bloco `## memória` com o que já se sabe sobre aquele chat (`--no-memory` desliga). Por ser byte-compatível, a mesma pasta pode ser lida pelo `memo` original em Python: `MEMORY_DIR=~/.claudewhats/memory ~/.optmem/memo wake`.

## Skill
Instalada automaticamente pelo plugin (`/plugin install claudewhats@claudewhats`); o conteúdo mora em `skills/claudewhats/SKILL.md`. Para usar sem o plugin, copie essa pasta para `~/.claude/skills/claudewhats/`.

## Smoke test no Windows — fique de olho
- **Daemon morrendo junto com o CLI pai.** Se quem chamou `claudewhats` roda dentro de um Job Object com kill-on-close (ex.: a ferramenta Bash do Claude Code), o daemon destacado morre quando o pai sai. Sintoma: `claudewhats status` diz que não há daemon logo após um comando que deveria tê-lo acordado. Correção: adicionar `CREATE_BREAKAWAY_FROM_JOB` (0x01000000) em `internal/daemon/spawn_windows.go`, com fallback sem a flag se der `ERROR_ACCESS_DENIED`.
- **Home com `#`, `?` ou `%`.** O caminho do banco entra cru numa URI `file:` do SQLite; esses caracteres quebram a abertura (`unable to open database`). Correção: `filepath.ToSlash` + `url.PathEscape` em `store.Open` e `wa.Open`, ou apontar `db_path` no config para uma pasta sem esses caracteres.
