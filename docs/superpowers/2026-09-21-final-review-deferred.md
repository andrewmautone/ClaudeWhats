# Final whole-branch review — feat/v1 (3b29295..3b9546f)

`go build ./...`, `go vet ./...`, `go test ./...` all pass (6 packages, 23 tests).

## Findings (ranked)

### Must fix before merge

1. **Path traversal in media save — security.** `internal/daemon/ingest.go:110-119`. `info.ID` (sender-controlled stanza `id` attr), `cl.Ext` (from document `fileName`; `internal/wa/classify.go:35` returns everything after the last `.`, e.g. `.\..\x`), and the chat user part go straight into `filepath.Join(MediaDir, user, id+ext)` + `os.WriteFile` with attacker content. Fix: sanitize `user`, `id`, `ext` to `[A-Za-z0-9._-]` (replace others with `_`, strip leading dots), then `rel, _ := filepath.Rel(in.MediaDir, path); if strings.HasPrefix(rel, "..") { return "", errors.New("bad media path") }`.

2. **Gemini API key leaks into logs/errors — security.** `internal/gemini/client.go:55` puts the key in the URL query. Any transport error from `c.HTTP.Do` embeds the full URL (`Post "https://...?key=AIza..."`), which the worker logs to `daemon.log` (`internal/daemon/worker.go:67`) and `summary` prints to the terminal. Fix: drop `?key=`, set `hr.Header.Set("x-goog-api-key", c.APIKey)`.

3. **DM conversations split across PN and LID chat keys.** `internal/daemon/ingest.go:46` keys `chats` by `info.Chat` verbatim. whatsmeow sets `Chat = X@lid` for LID-addressed DMs (`whatsmeow/message.go:178`; the PN is only in `SenderAlt`/`RecipientAlt`), and history-sync `conv.GetID()` can be a LID too. Same contact → two `chats` rows; `read <name>` fails with `ambiguous "maria": Maria, Maria`; `search --chat`/`summary --chat` see half the messages; `sync` requests against the wrong key. Fix: canonicalize DM chat key to the PN in `Ingester.OnMessage` — when `info.Chat.Server == "lid"`, use `RecipientAlt` (from-me) / `SenderAlt` (incoming, chat==sender) if set, else a new `Store.PNForLID(lid)` (pn identity sharing the LID's `contact_id`); in `internal/wa/client.go:66` prefer `conv.GetPnJID()` over `GetID()` when non-empty. This also covers the spec's "mapeamento LID↔PN via evento ou store.LIDs" line, otherwise unimplemented (no `events.LIDMigration`/`store.LIDs` consumer).

4. **Status broadcasts and newsletters are ingested and sent to Gemini.** No server filter in `internal/daemon/ingest.go:44`; every contact's status image/audio becomes a `dm` chat `status@broadcast`, is downloaded and transcribed (Gemini cost + privacy noise); `@newsletter` channels land as `dm`. Fix: at the top of `OnMessage`: `if info.Chat.Server == types.BroadcastServer || info.Chat.Server == types.NewsletterServer { return }`.

### Should fix (small, high value)

5. **Missing Gemini key permanently marks media as `failed`.** `internal/daemon/worker.go:66-70`: with an empty key every job returns `gemini.ErrNoKey`, burns 5 attempts, and is set `failed`; after the user configures the key those messages are never transcribed. Fix: `if errors.Is(err, gemini.ErrNoKey) { return false, err }` (no `FailJob`), or skip starting the worker in `Run` when `cfg.GeminiAPIKey == ""` and log once.

6. **Identity resolution races → orphan contacts.** `internal/store/contacts.go:112` `ResolveIdentity` (and `merge`, `LinkContacts`) run 3-6 statements without a tx; `OnMessage` is called concurrently from the whatsmeow receive loop, the history-sync goroutine (`whatsmeow/message.go:712`) and `syncGroups`. Two first-messages from the same unknown sender both see `contactOf==0`, both `newContact`; the second `putIdentity` overwrites `contact_id` → empty orphan contact in `contact list`. Fix: wrap in a `BEGIN IMMEDIATE` tx (with `SetMaxOpenConns(1)` the tx also serializes callers). Note: `SetMaxOpenConns(1)` itself is deadlock-safe — every query path closes `*sql.Rows` before calling `ContactName` (verified `ListChats`, `scanMessages`, `ResolveChat`, `findContact`); HTTP handlers use `QueryRow`/closed rows only.

7. **Two `*sql.DB` handles on one file.** store (pool 1) + whatsmeow (unbounded), both WAL with `busy_timeout(5000)`. Under WAL a deferred `BEGIN` that reads then writes gets `SQLITE_BUSY` immediately (busy handler not consulted), so whatsmeow txs can fail with "database is locked" while the store is writing. Mitigation: append `&_txlock=immediate` to both DSNs (`internal/store/store.go:17`, `internal/wa/client.go:36`).

8. **Background daemon loses whatsmeow logs and panics.** `internal/daemon/server.go:186` `waLog.Stdout(...)` writes to stdout; detached mode has stdout/stderr = NUL, so WA warnings and Go panics vanish. Fix: in `background`, set `os.Stdout, os.Stderr = f, f` before constructing the WA logger (spec: "stdout/err em daemon.log").

9. **Presence `unavailable` silently dropped right after pairing.** `internal/wa/client.go:88` ignores the error; `SendPresence` returns `ErrNoPushName` until app-state sync fills `Store.PushName` (`whatsmeow/presence.go:67`). Fix: also send on `*events.AppStateSyncComplete` (critical block) / `*events.PushNameSetting`, and log the error.

10. **Spec handler gap: `*events.Contact` not handled** (`internal/wa/client.go:60-93`). Address-book names (FullName) never reach `contacts`, so `read <name>` only works for push names or manual contacts. Add a case that sets the contact name when `auto=1`.

11. **`ResolveChat` cannot match by push name** (`internal/store/messages.go:230-235` LIKEs only `contacts.name`). A contact first seen via `OnGroup` members gets `name=''`; a later `PushName` event only updates `identities.push_name`, so `read <pushname>` fails while `chats` displays that name. Add `OR lower(i.push_name) LIKE '%'||lower(?)||'%'` in the same subquery. (This is the deferred "auto-contact name = push_name coupling" minor — it does bite.)

### Low / smoke-test watch items

12. Windows spawn under a Job Object: if the invoking tool runner (e.g. Claude Code's Bash tool) uses a kill-on-close job, the detached daemon dies with the parent. If the smoke test shows that, add `CREATE_BREAKAWAY_FROM_JOB (0x01000000)` to `internal/daemon/spawn_windows.go:16` with a fallback without it on `ERROR_ACCESS_DENIED`. Handle inheritance is fine (Go restricts to std handles, which are NUL here).
13. `store.Open`/`wa.Open` embed a raw Windows path in a `file:` URI; a home path with `#`, `?` or `%` breaks. `filepath.ToSlash` + `url.PathEscape`. Watch in smoke test.
14. `internal/cli/search.go:47` prints `ContactName(chatJID)` for groups → digits instead of group name; look up `chats.name` first.
15. `internal/store/contacts.go:206` `findContact` ignores `rows.Scan`/`rows.Err`.
16. `internal/daemon/server.go:221` handles only `os.Interrupt`; add `syscall.SIGTERM` on unix for the pm2/service use case.
17. FTS external-content over a table without `INTEGER PRIMARY KEY`: `VACUUM` may renumber rowids and desync `messages_fts`. Nothing vacuums today; note it or add a `rebuild` maintenance command later. `INSERT OR IGNORE` vs triggers is fine (ignored rows fire no trigger).
18. History-sync stub messages (nil `Message`) are stored as `type=other` with empty text — clutter in `read`. Skip in `wa/client.go:71` when `hm.GetMessage().GetMessage() == nil`.
19. `send` to a number with no `chats` row errors "chat not found"; spec implies number resolution — consider fallback to `<digits>@s.whatsapp.net` when an identity exists (`internal/store/messages.go:213`).
20. `summary` with no key surfaces `ErrNoKey` only after opening the store and listing chats; fine, but a pre-check gives a clearer error.

## Spec alignment (everything else checked)

CLI+daemon / no MCP ✓; read commands DB-only ✓ (`status` only probes loopback HTTP); detached spawn + log + pidfile + 30m idle + `--idle 0` ✓; port bind acts as the daemon lock ✓; `serve` foreground QR ✓; background unpaired → `ErrNotPaired` in log, surfaced by `EnsureRunning` log tail ✓; `LoggedOut → shutdown`, never re-pairs (background passes `showQR=nil`) ✓; no `MarkRead` anywhere ✓; schema matches spec exactly ✓; worker 1-at-a-time, 30s·2ⁿ backoff, 5 attempts → `failed` ✓; HTTP `/status /send /sync /shutdown` loopback-only ✓; `sync` builds `BuildHistorySyncRequest` from oldest known message ✓; `send` confirmation + `--yes` ✓; all commands `--json` ✓; Gemini prompts/model/key sources ✓; tests per spec (store, identity, gemini httptest, cli in-memory) ✓; `EnsureRunning` polls 300ms up to 20s (spec says 15s — fine); shutdown ordering (HTTP → cancel → wg.Wait → Disconnect → store.Close) ✓; `Connect` ctx is the daemon-lifetime ctx ✓; CLI reads while the daemon writes are safe under WAL ✓.

## Ledger "minor (deferred)" triage

Fix before merge:
- ResolveChat name ↔ push_name coupling → finding 11.
- SKILL.md send rule only in the command line, not in Rules → add rule 6: "`send` só com pedido explícito do usuário; confirme chat e texto antes de `--yes`". Claude reads the Rules list.

Keep (already fixed or harmless):
- T13 `runWith` flag reset — already fixed by `resetFlags()` (`internal/cli/common.go:96`).
- `FailJob` doesn't clear transcript — transcript is empty for pending rows anyway.
- jobs.go backoff comment — now matches code.
- `UpsertChat` updates kind on conflict — kind is a function of the jid server, stable.
- sticker QuotedID — fixed (`internal/wa/classify.go:78`).
- anonymous `Contents` struct — style.
- no wa tests — spec says manual.
- ingest video/document → skipped untested — cheap to add, not blocking.
- `/shutdown` no Touch — fixed (`internal/daemon/server.go:147`).
- swallowed errors on Stats/OldestMessage/ParseJID/SetTranscript — non-fatal paths; pidfile write is now logged.
- IdleLoop test timing margins — passing; revisit only if flaky.
- send/sync close store early — correct (store must not stay open across the daemon spawn).
- send prompt ReadString error → "no" — safe default.

## Verdict

Not ready to merge as-is. The architecture is sound and matches the spec (wiring, shutdown ordering, single-connection store discipline, loopback API, lifecycle), and the per-task reviews were thorough on their slices. The blockers are all cross-cutting and small (~60-80 lines total): a path-traversal write from wire-controlled message IDs/extensions (1), the Gemini key in URLs that end up in `daemon.log` and terminal output (2), DM chats fragmenting across LID/PN keys — which breaks `read`/`summary` by name for LID-addressed contacts, the common case on current WhatsApp (3), and unfiltered status-broadcast ingestion burning Gemini quota on every contact's stories (4). Fix 1-4 plus the two ledger items (ideally 5-7 too), rerun tests, then it is ready for the human smoke test; items 12-13 are what to watch during that smoke test on Windows.
