---
name: claudewhats
description: Lê, busca, resume e envia mensagens de WhatsApp do usuário via o CLI `claudewhats` (banco local SQLite + transcrição Gemini). Use quando o usuário pedir para ver conversas, resumir o zap, achar uma mensagem, ver o que alguém mandou, puxar histórico de um chat, mandar mensagem no WhatsApp, ou cadastrar/vincular contatos.
---

# ClaudeWhats

Tudo é lido do banco local; nada aqui abre janela. Sempre use `--json` para parsear.

## Comandos

- `claudewhats status --json` — daemon rodando? conectado? quantas msgs.
- `claudewhats chats [--since 7d] [--kind dm|group] --json` — conversas com atividade (kind dm|group, count, jid).
- `claudewhats read "<chat>" [--since 24h] [--limit 200] --json` — `<chat>` = jid, número, nome do contato ou do grupo. Cada msg tem `type` (text|audio|image|...) e `transcript` (áudio transcrito / imagem descrita). `transcript_status` pending = Gemini ainda não processou.
- `claudewhats search "<texto>" [--chat X] [--since 7d] [--limit 50] --json` — full-text em texto+transcrições.
- `claudewhats summary [--since 24h] [--chat X] [--limit 500] --json` — Gemini resume por chat + geral. Para muitos chats prefira isto a ler tudo.
- `claudewhats sync "<chat>" [--count 50]` — pede histórico antigo ao celular; espere ~10s e rode `read` de novo.
- `claudewhats send "<chat>" "<texto>" --yes` — envia texto. **Só com pedido explícito do usuário, e confirme o chat e o texto com ele antes de usar `--yes`.**
- `claudewhats contact add "<nome>" "<numero>"` / `contact list [--q filter] --json` / `contact link <a> <b>` / `contact rename <ref> <nome>`.
- `claudewhats serve [--background] [--idle 30m]` — inicia o daemon (background mode com log e idle timeout).
- `claudewhats stop` — encerra o daemon em background.

## Regras

1. Leitura primeiro: `chats` → `read`/`search`/`summary`. Nunca peça ao usuário para abrir terminal do daemon; ele sobe sozinho em background.
2. Se um comando erra com "não pareado", diga ao usuário para rodar `claudewhats serve` uma vez e escanear o QR.
3. `contact link` só quando o usuário pedir explicitamente para juntar duas identidades (ex.: "o 55...@lid do grupo é a Maria").
4. Se `transcript_status` vier `pending` em muitas mensagens, avise que a transcrição ainda está rodando e ofereça tentar de novo.
5. Ambiguidade no `<chat>` volta erro listando candidatos: pergunte ao usuário qual.
6. `send` só com pedido explícito do usuário: confirme com ele o chat e o texto exatos antes de rodar com `--yes`. Nunca envie por iniciativa própria.
