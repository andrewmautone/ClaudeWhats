# ClaudeWhats — design

Data: 2026-09-21

## Objetivo

Binário Go (`claudewhats`) que conecta numa conta WhatsApp (whatsmeow, multi-device), grava
todas as mensagens num SQLite local, transcreve áudio e descreve imagens com Gemini na
ingestão, e expõe subcomandos de CLI que o Claude usa através de uma skill (`skill/SKILL.md`)
para ler conversas, buscar, resumir, sincronizar histórico de um chat, enviar texto e
gerenciar contatos/identidades.

## Decisões

- Interface: CLI + daemon. Sem MCP.
- Leitura sempre do banco: `chats/read/search/summary/contact/status` só abrem o SQLite e
  nunca conectam no WhatsApp. Só `send`, `sync` e a ingestão precisam do daemon.
- Daemon invisível e auto-gerenciado: nenhum comando exige um terminal aberto com `serve`.
  Quem precisa do daemon faz auto-spawn (processo detached, sem janela, log em arquivo) e
  o daemon encerra sozinho após `idle_timeout` (default 30m) sem comandos. O WhatsApp
  entrega o backlog offline na reconexão, então mensagens do período morto chegam
  atrasadas, não se perdem. `serve` fica para o primeiro pareamento (QR) e para quem
  quiser rodá-lo fixo (pm2/serviço) — nesse caso o auto-spawn é no-op.
- Lib WhatsApp: `go.mau.fi/whatsmeow`. Sessão no mesmo SQLite (`sqlstore`).
- SQLite sem cgo: `modernc.org/sqlite`. WAL ligado; comandos de leitura abrem o banco
  em paralelo ao daemon.
- Gemini: chamado na ingestão para todo áudio/imagem; resultado guardado em `transcript`.
  `summary` também usa Gemini.
- Histórico: só daqui pra frente por padrão. `sync <chat>` pede histórico sob demanda.
- Identidade: resolução automática LID↔número sempre; `contact link` manual só quando o
  usuário pedir.
- Escrita: `send <chat> <texto>` permitido, com confirmação (`--yes` para pular).
- Presença: `unavailable` ao conectar e após qualquer envio; nunca marca como lida
  (evita sumir notificação no celular).

## Layout

```
cmd/claudewhats/main.go
internal/config/      # ~/.claudewhats/config.yaml + env (GEMINI_API_KEY)
internal/store/       # schema, migrations, queries; testes com SQLite em memória
internal/identity/    # resolve JID -> contact_id; merge de contatos
internal/gemini/      # cliente REST: transcribe(audio), describe(image), summarize(text)
internal/wa/          # whatsmeow: connect, QR, handlers, media download, send, history request
internal/daemon/      # serve: junta wa + store + fila Gemini + HTTP loopback
internal/cli/         # subcomandos (cobra), saída texto ou --json
skill/SKILL.md
```

Dados em `~/.claudewhats/`: `config.yaml`, `data.db`, `media/<chat>/<msgid>.<ext>`.

## Schema

```sql
contacts(id INTEGER PK, name TEXT, notes TEXT, auto INTEGER, created_at INTEGER)
identities(jid TEXT PK, contact_id INTEGER FK, kind TEXT CHECK(pn|lid), push_name TEXT)
chats(jid TEXT PK, kind TEXT CHECK(dm|group), name TEXT, last_msg_at INTEGER)
group_members(chat_jid TEXT, jid TEXT, PK(chat_jid, jid))
messages(
  id TEXT, chat_jid TEXT, PK(chat_jid, id),
  sender_jid TEXT, ts INTEGER, from_me INTEGER,
  type TEXT,            -- text|audio|image|video|document|sticker|reaction|location|contact|other
  text TEXT,            -- texto ou caption
  media_path TEXT, media_mime TEXT,
  transcript TEXT, transcript_status TEXT, -- pending|done|failed|skipped
  quoted_id TEXT, raw_json TEXT
)
messages_fts (FTS5 sobre text, transcript; content=messages)
gemini_jobs(id INTEGER PK, chat_jid, msg_id, attempts, next_at, last_error)
```

`identities.kind='lid'` e `'pn'` da mesma pessoa compartilham `contact_id`. Quando o
whatsmeow entrega mapeamento LID↔PN (evento ou `store.LIDs`), a identidade LID passa a
apontar pro contato do PN; se ambos já tinham contato, funde (o com `auto=0` vence).

## Daemon (`serve`)

1. Carrega config, abre store, roda migrations.
2. whatsmeow: `sqlstore` no mesmo banco; se sem sessão, mostra QR no terminal.
3. Handlers:
   - `*events.Message`: upsert chat/identidade, insere mensagem com `type`; se mídia
     (audio/image), baixa para `media/` e cria `gemini_jobs`. Vídeo/documento/sticker: salva
     mídia, `transcript_status='skipped'`.
   - `*events.HistorySync`: mesmo caminho para cada mensagem (mídia baixada quando possível).
   - `*events.GroupInfo` / `*events.Contact` / push names: atualiza `chats`, `group_members`,
     `identities.push_name`.
   - Mapeamento LID↔PN: sincroniza `identities`.
4. Worker Gemini: 1 job por vez, backoff exponencial, máx 5 tentativas → `failed`.
5. HTTP em `127.0.0.1:<port>` (porta no config, default 7411): `POST /send`, `POST /sync`,
   `GET /status`. Sem auth além de loopback.
6. Após conectar e após cada envio: `SendPresence(unavailable)`.
7. Ciclo de vida:
   - `serve` (foreground): QR no terminal se não pareado; roda até Ctrl+C, sem idle timeout.
   - `serve --background` (usado pelo auto-spawn): sem console (Windows: `CREATE_NO_WINDOW`
     + `DETACHED_PROCESS`; Unix: `Setsid`), stdout/err em `~/.claudewhats/daemon.log`,
     pidfile `daemon.pid`. Se não pareado, sai com erro no log e o CLI avisa
     "rode `claudewhats serve` uma vez para parear".
   - Idle: cada request HTTP renova o timer; sem request por `idle_timeout` → desconecta
     limpo e sai. `serve --background --idle 0` = nunca sai.
   - Auto-spawn (`internal/daemon/client.go`): `GET /status`; se falha, lança
     `serve --background` e espera até 15s pelo `/status`. Lock por pidfile evita dois
     daemons.
   - `stop`: comando que pede `POST /shutdown`.

## Comandos

Todos aceitam `--json`. `<chat>` resolve por: JID exato, número, nome de contato, nome de
grupo (case-insensitive, substring; ambíguo → erro listando candidatos).

| comando | comportamento |
|---|---|
| `serve [--background] [--idle 30m]` | daemon acima |
| `stop` | encerra o daemon em background |
| `status` | daemon vivo? conectado? total msgs, jobs pendentes |
| `chats [--since 7d] [--kind dm\|group]` | chats ordenados por `last_msg_at`, com contagem no período |
| `read <chat> [--since 24h] [--limit 200]` | mensagens cronológicas: `ts sender(type): text/transcript` |
| `search <texto> [--chat X] [--since]` | FTS5 |
| `summary [--since 24h] [--chat X]` | monta o texto das mensagens do período (por chat), manda pro Gemini, imprime resumo por chat + geral |
| `sync <chat> [--count 50]` | via daemon: `BuildHistorySyncRequest` a partir da msg mais antiga conhecida |
| `send <chat> <texto> [--yes]` | via daemon; sem `--yes` pede confirmação no terminal |
| `contact add <nome> <numero>` | cria contato + identidade pn |
| `contact list [--q]` | contatos com suas identidades |
| `contact link <a> <b>` | `a`,`b` = jid ou nome; funde num contato só |
| `contact rename <a> <nome>` | |

## Gemini

REST `generativelanguage.googleapis.com`, modelo configurável (default `gemini-2.5-flash`).
- `Transcribe(audio, mime)` → texto (prompt: transcreva fielmente em pt-BR, sem comentários).
- `Describe(image, mime, caption)` → descrição curta + texto visível.
- `Summarize(chunks)` → resumo; se input > limite, resume por chat e depois o conjunto.

## Erros

- Gemini falha: job fica `pending` com retry; mensagem já está no banco com `type` e mídia.
- Daemon fora: `send`/`sync` erram com "daemon não está rodando (claudewhats serve)".
- Desconexão do WhatsApp: whatsmeow reconecta; `LoggedOut` → loga e encerra pedindo re-pareamento (nunca re-pareia sozinho).

## Testes

- `store`: SQLite em memória — insert/upsert, FTS, queries por período, resolução de chat.
- `identity`: casos LID sem PN, PN chega depois, merge manual, conflito auto vs manual.
- `gemini`: `httptest.Server` fake.
- `cli`: testes dos comandos sobre store em memória (sem daemon).
- `wa`: só manual.

## Fora de escopo (v1)

Envio de mídia, reações, MCP, multi-conta, UI.
