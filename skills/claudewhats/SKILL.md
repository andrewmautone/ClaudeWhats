---
name: claudewhats
description: Lê, busca, resume e envia mensagens de WhatsApp do usuário via o CLI `claudewhats` (banco local SQLite + transcrição Gemini). Use quando o usuário pedir para ver conversas, resumir o zap, achar uma mensagem, ver o que alguém mandou, puxar histórico de um chat, mandar mensagem no WhatsApp, ou cadastrar/vincular contatos.
---

# ClaudeWhats

Tudo é lido do banco local; nada aqui abre janela. Sempre use `--json` para parsear.

## Setup

O comando `claudewhats` vem do `bin/` deste plugin e baixa o binário da release na primeira execução (fica em `~/.claudewhats/bin/`).

Pareamento (primeira vez, ou depois de um logout): se `claudewhats status --json` mostrar `paired: false` (ou qualquer comando falhar por "não pareado"):

1. Avise o usuário que cada QR expira em ~20-60 s: ele deve abrir WhatsApp > Aparelhos conectados > Conectar aparelho e estar com a câmera pronta. Rode `claudewhats pair --json` — o daemon sobe em background e abre sozinho, no navegador padrão, uma página (`qr_html`) que recarrega o QR a cada 2 s (ela mesma cuida do QR expirar, então não precisa repetir passos). Se o usuário disser que nada abriu, passe o caminho de `qr_html` para ele abrir manualmente.
2. Rode `claudewhats pair --wait --json` (bloqueia até 3 min). A saída é um JSON por linha: cada `{"event":"qr","qr_png":...}` é um QR novo (a página já mostra sozinha); termina com `{"paired":true}` — a mesma aba passa a exibir "Pareado".
3. Se expirar ("tempo esgotado"), repita do passo 1.

Fallback se abrir o navegador falhar (ambiente sem GUI, `--open=false`, etc.): use a ferramenta Read no caminho `qr_png` para mostrar o QR ao usuário. Alternativa no terminal do usuário: `claudewhats serve` (foreground) imprime o QR direto no terminal.

## Comandos

- `claudewhats status --json` — daemon rodando? conectado? `paired`/`pairing` (+ `qr_png` enquanto pareia)? quantas msgs.
- `claudewhats chats [--since 7d] [--kind dm|group] --json` — conversas com atividade (kind dm|group, count, jid).
- `claudewhats read "<chat>" [--since 24h] [--until 2026-09-21] [--limit 200] [--out[=arquivo]] [--no-memory] --json` — `<chat>` = jid, número, nome do contato ou do grupo. `--until` limita o período por cima (mesma sintaxe de `--since`; uma data sozinha vale até o fim daquele dia). `--limit 0` = sem limite (todas as mensagens do período). `--out` (sem caminho) salva a saída (texto ou JSON, conforme `--json`) num arquivo na pasta de exports do claudewhats; `--out=<arquivo>` salva exatamente nesse caminho (dando um valor explícito é preciso usar `=`, não espaço). Em ambos os casos não acorda o daemon e imprime só `salvo: <caminho> (<N> mensagens)` (JSON: `{"saved": ..., "count": ...}`). `--json` (sem `--out`) retorna `{"chat": {...}, "memory": [...], "messages": [...]}`; `memory` são as linhas de memória relevantes a esse chat (vazio se nada bater, ou com `--no-memory`). Cada msg tem `type` (text|audio|image|...) e `transcript` (áudio transcrito / imagem descrita). `transcript_status` pending = Gemini ainda não processou. Em modo texto, quando há memória relevante ela aparece antes das mensagens sob `## memória`.
- `claudewhats search "<texto>" [--chat X] [--since 7d] [--until 2026-09-21] [--limit 50] --json` — full-text em texto+transcrições. `--limit 0` = sem limite.
- `claudewhats summary [--since 24h] [--until 2026-09-21] [--chat X] [--limit 500] [--no-memory] --json` — Gemini resume por chat + geral. Para muitos chats prefira isto a ler tudo. Com `--chat X`, inclui o mesmo bloco de memória daquele chat (texto e campo JSON `memory`).
- `claudewhats sync "<chat>" [--count 50]` — pede histórico antigo ao celular; espere ~10s e rode `read` de novo.
- `claudewhats send "<chat>" "<texto>" --yes` — envia texto. **Só com pedido explícito do usuário, e confirme o chat e o texto com ele antes de usar `--yes`.**
- `claudewhats contact add "<nome>" "<numero>"` / `contact list [--q filter] --json` / `contact link <a> <b>` / `contact rename <ref> <nome>`.
- `claudewhats pair [--wait] [--open=true] [--timeout 3m] --json` — pareia via página HTML (abre sozinha no navegador, `--open=false` desliga): `{paired, pairing, qr_png, qr_txt, qr_html, qr_updated_at}`; com `--wait` imprime um JSON por linha (`{"event":"qr","qr_png":...,"qr_html":...}` a cada QR novo) e termina com `{"paired":true}`.
- `claudewhats serve [--background] [--idle 30m]` — inicia o daemon (background mode com log e idle timeout; foreground imprime o QR no terminal).
- `claudewhats stop` — encerra o daemon em background.

### Memória (`claudewhats memory`)

- `claudewhats memory wake [part] [T]` — lê a memória inteira (paginada); segue exatamente o que a saída pedir, inclusive `wake 2 N` para a próxima parte e os `nap` pedidos.
- `claudewhats memory note "<texto>"` — registra uma memória (uma linha, ≤280 bytes). `--json` retorna `{"id": N, "nap": "..."}`.
- `claudewhats memory nap [a-b] ["<texto>"]` — salva a compressão de um bloco pendente (copie o `a-b` exatamente do que `wake`/`nap` imprimiu) e mostra a próxima pendência.
- `claudewhats memory recall <regex>` — busca em toda a memória; `--json` retorna `{"lines": [...]}`.
- `claudewhats memory zoom <a-b>` — abre um nó da árvore de memória (suas duas metades).
- `claudewhats memory forget <a-b>` — descarta uma compressão e tudo construído sobre ela.

Cada comando de memória imprime exatamente o que a ferramenta manda (inclusive mensagens de erro); leia o texto até o fim antes de continuar.

## Regras

1. **A resposta ao usuário é sempre o conteúdo que ele pediu** (as mensagens, o resumo, a confirmação do envio) — escrita por você, no chat, completa. Nunca deixe o conteúdo só dentro da saída de uma ferramenta.
2. **Memória é bastidor.** Antes de qualquer leitura rode `claudewhats memory wake` e faça o que ele imprimir até o fim (inclusive `wake 2 N` e os `nap` pedidos). Quando aprender algo com efeito duradouro sobre uma pessoa, grupo ou assunto — decisão, combinado, preferência, fato, "grupo do X = ..." — registre `claudewhats memory note "<1 linha, ≤280 bytes, cite o nome>"`; se pedir compressão, faça na hora. Tudo isso é silencioso: **não narre** que anotou, comprimiu ou acordou a memória, e não transforme a memória em assunto da resposta. Não registre o que já está na memória.
3. Leitura primeiro: `chats` → `read`/`search`/`summary`. Nunca peça ao usuário para abrir terminal do daemon; ele sobe sozinho em background.
4. Se `status --json` mostrar `paired: false` ou um comando errar com "não pareado", siga o fluxo de pareamento do Setup: usuário com a câmera pronta → `pair --json` (abre o navegador sozinho) → `pair --wait --json`. Se o navegador não abriu, passe o `qr_html` (ou faça Read no `qr_png`) para o usuário. Repita se expirar.
5. `contact link` só quando o usuário pedir explicitamente para juntar duas identidades (ex.: "o 55...@lid do grupo é a Maria").
6. Se `transcript_status` vier `pending` em muitas mensagens, avise que a transcrição ainda está rodando e ofereça tentar de novo.
7. Ambiguidade no `<chat>` volta erro listando candidatos: pergunte ao usuário qual.
8. `send` só com pedido explícito do usuário: confirme com ele o chat e o texto exatos antes de rodar com `--yes`. Nunca envie por iniciativa própria.
9. O usuário pode querer as mensagens **cruas** de um período: entregue-as como estão (sem resumir) — coladas no chat quando couberem, ou exportadas quando ele pedir para salvar/exportar ou quando for muito (centenas de mensagens). Para exportar use `--out` (sem caminho): o arquivo vai para a pasta de exports do claudewhats e você pode lê-lo com Read; só passe um caminho em `--out=<caminho>` quando o usuário disser onde quer o arquivo. Só resuma quando ele pedir resumo. Períodos: `--since`/`--until` aceitam `24h`, `7d` ou datas `2026-09-01`; para "de X até Y dias atrás" use `--since Yd --until Xd`.
