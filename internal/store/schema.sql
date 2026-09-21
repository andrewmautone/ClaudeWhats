CREATE TABLE IF NOT EXISTS contacts (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  auto INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS identities (
  jid TEXT PRIMARY KEY,
  contact_id INTEGER NOT NULL REFERENCES contacts(id),
  kind TEXT NOT NULL CHECK (kind IN ('pn','lid')),
  push_name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS identities_contact ON identities(contact_id);
CREATE TABLE IF NOT EXISTS chats (
  jid TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('dm','group')),
  name TEXT NOT NULL DEFAULT '',
  last_msg_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS group_members (
  chat_jid TEXT NOT NULL,
  jid TEXT NOT NULL,
  PRIMARY KEY (chat_jid, jid)
);
CREATE TABLE IF NOT EXISTS messages (
  chat_jid TEXT NOT NULL,
  id TEXT NOT NULL,
  sender_jid TEXT NOT NULL,
  ts INTEGER NOT NULL,
  from_me INTEGER NOT NULL DEFAULT 0,
  type TEXT NOT NULL,
  text TEXT NOT NULL DEFAULT '',
  media_path TEXT NOT NULL DEFAULT '',
  media_mime TEXT NOT NULL DEFAULT '',
  transcript TEXT NOT NULL DEFAULT '',
  transcript_status TEXT NOT NULL DEFAULT 'skipped',
  quoted_id TEXT NOT NULL DEFAULT '',
  raw_json TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (chat_jid, id)
);
CREATE INDEX IF NOT EXISTS messages_ts ON messages(ts);
CREATE INDEX IF NOT EXISTS messages_chat_ts ON messages(chat_jid, ts);
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(text, transcript, content='messages', content_rowid='rowid');
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, text, transcript) VALUES (new.rowid, new.text, new.transcript);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text, transcript) VALUES ('delete', old.rowid, old.text, old.transcript);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text, transcript) VALUES ('delete', old.rowid, old.text, old.transcript);
  INSERT INTO messages_fts(rowid, text, transcript) VALUES (new.rowid, new.text, new.transcript);
END;
CREATE TABLE IF NOT EXISTS gemini_jobs (
  id INTEGER PRIMARY KEY,
  chat_jid TEXT NOT NULL,
  msg_id TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  UNIQUE (chat_jid, msg_id)
);
