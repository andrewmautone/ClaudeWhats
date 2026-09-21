package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Message struct {
	ChatJID          string `json:"chat_jid"`
	ID               string `json:"id"`
	SenderJID        string `json:"sender_jid"`
	TS               int64  `json:"ts"`
	FromMe           bool   `json:"from_me"`
	Type             string `json:"type"`
	Text             string `json:"text,omitempty"`
	MediaPath        string `json:"media_path,omitempty"`
	MediaMime        string `json:"media_mime,omitempty"`
	Transcript       string `json:"transcript,omitempty"`
	TranscriptStatus string `json:"transcript_status"`
	QuotedID         string `json:"quoted_id,omitempty"`
	RawJSON          string `json:"raw_json,omitempty"`
	Sender           string `json:"sender"` // display, filled by queries
}

type Chat struct {
	JID       string `json:"jid"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	LastMsgAt int64  `json:"last_msg_at"`
	Count     int64  `json:"count"`
}

func (s *Store) UpsertChat(jid, kind, name string) error {
	_, err := s.db.Exec(`INSERT INTO chats(jid, kind, name) VALUES (?,?,?)
		ON CONFLICT(jid) DO UPDATE SET kind=excluded.kind, name=CASE WHEN excluded.name<>'' THEN excluded.name ELSE chats.name END`,
		jid, kind, name)
	return err
}

func (s *Store) SetGroupMembers(chatJID string, jids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing members
	if _, err := tx.Exec(`DELETE FROM group_members WHERE chat_jid=?`, chatJID); err != nil {
		return err
	}
	// Insert new members
	for _, jid := range jids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO group_members(chat_jid, jid) VALUES (?,?)`, chatJID, jid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) InsertMessage(m Message) (inserted bool, err error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO messages(chat_jid, id, sender_jid, ts, from_me, type, text, media_path, media_mime, transcript, transcript_status, quoted_id, raw_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ChatJID, m.ID, m.SenderJID, m.TS, boolToInt(m.FromMe), m.Type, m.Text, m.MediaPath, m.MediaMime, m.Transcript, m.TranscriptStatus, m.QuotedID, m.RawJSON)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	// Update last_msg_at in chats using max() to prevent backfill from moving it backwards
	_, err = s.db.Exec(`UPDATE chats SET last_msg_at=max(last_msg_at, ?) WHERE jid=?`, m.TS, m.ChatJID)
	if err != nil {
		return false, err
	}
	return true, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) SetTranscript(chatJID, id, transcript, status string) error {
	_, err := s.db.Exec(`UPDATE messages SET transcript=?, transcript_status=? WHERE chat_jid=? AND id=?`, transcript, status, chatJID, id)
	return err
}

func (s *Store) ListChats(since int64, kind string) ([]Chat, error) {
	query := `SELECT c.jid, c.kind, c.name, c.last_msg_at,
		(SELECT count(*) FROM messages m WHERE m.chat_jid=c.jid AND m.ts>=?)
		FROM chats c WHERE c.last_msg_at>=?`
	params := []interface{}{since, since}

	if kind != "" {
		query += ` AND c.kind=?`
		params = append(params, kind)
	}
	query += ` ORDER BY c.last_msg_at DESC`

	rows, err := s.db.Query(query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.JID, &c.Kind, &c.Name, &c.LastMsgAt, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fill in display names after closing the rows to avoid deadlock with SetMaxOpenConns(1)
	for i := range out {
		if out[i].Name == "" {
			out[i].Name = s.ContactName(out[i].JID)
		}
	}
	return out, nil
}

const msgCols = `chat_jid,id,sender_jid,ts,from_me,type,text,media_path,media_mime,transcript,transcript_status,quoted_id`

func (s *Store) scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var fm int
		if err := rows.Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.TS, &fm, &m.Type, &m.Text, &m.MediaPath, &m.MediaMime, &m.Transcript, &m.TranscriptStatus, &m.QuotedID); err != nil {
			return nil, err
		}
		m.FromMe = fm == 1
		if m.FromMe {
			m.Sender = "eu"
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fill in sender names after closing rows to avoid deadlock with SetMaxOpenConns(1)
	for i := range out {
		if out[i].Sender == "" {
			out[i].Sender = s.ContactName(out[i].SenderJID)
		}
	}
	return out, nil
}

func (s *Store) ReadMessages(chatJID string, since int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM (SELECT * FROM messages WHERE chat_jid=? AND ts>=? ORDER BY ts DESC LIMIT ?) ORDER BY ts ASC`, chatJID, since, limit)
	if err != nil {
		return nil, err
	}
	return s.scanMessages(rows)
}

func (s *Store) Search(q, chatJID string, since int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages WHERE rowid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)
		AND (?='' OR chat_jid=?) AND ts>=? ORDER BY ts DESC LIMIT ?`, ftsQuery(q), chatJID, chatJID, since, limit)
	if err != nil {
		return nil, err
	}
	return s.scanMessages(rows)
}

// ftsQuery quotes each word so punctuation never breaks FTS5 syntax.
func ftsQuery(q string) string {
	words := strings.Fields(q)
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return strings.Join(words, " AND ")
}

// ResolveChat resolves a reference to a chat: exact jid, number->dm, or substring name.
func (s *Store) ResolveChat(ref string) (Chat, error) {
	var c Chat

	// Try exact jid match
	err := s.db.QueryRow(`SELECT jid, kind, name, last_msg_at FROM chats WHERE jid=?`, ref).
		Scan(&c.JID, &c.Kind, &c.Name, &c.LastMsgAt)
	if err == nil {
		if c.Name == "" {
			c.Name = s.ContactName(c.JID)
		}
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}

	// Try phone number -> dm jid
	if d := digits(ref); d != "" && d == strings.TrimPrefix(ref, "+") {
		dmJID := d + "@s.whatsapp.net"
		err = s.db.QueryRow(`SELECT jid, kind, name, last_msg_at FROM chats WHERE jid=?`, dmJID).
			Scan(&c.JID, &c.Kind, &c.Name, &c.LastMsgAt)
		if err == nil {
			if c.Name == "" {
				c.Name = s.ContactName(c.JID)
			}
			return c, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return c, err
		}
	}

	// Try substring match on name and contact names
	rows, err := s.db.Query(`SELECT c.jid, c.kind, c.name, c.last_msg_at,
		(SELECT con.name FROM contacts con JOIN identities i ON i.contact_id=con.id WHERE i.jid=c.jid LIMIT 1)
		FROM chats c
		WHERE lower(c.name) LIKE '%'||lower(?)||'%'
		   OR CAST((SELECT lower(con.name) FROM contacts con JOIN identities i ON i.contact_id=con.id WHERE i.jid=c.jid LIMIT 1) AS TEXT) LIKE '%'||lower(?)||'%'
		   OR EXISTS (SELECT 1 FROM identities i WHERE i.jid=c.jid AND lower(i.push_name) LIKE '%'||lower(?)||'%')
		ORDER BY (lower(c.name)=lower(?)) DESC, c.last_msg_at DESC LIMIT 5`, ref, ref, ref, ref)
	if err != nil {
		return c, err
	}
	defer rows.Close()

	var chats []Chat
	var contactNames []string
	for rows.Next() {
		var ch Chat
		var contactName sql.NullString
		if err := rows.Scan(&ch.JID, &ch.Kind, &ch.Name, &ch.LastMsgAt, &contactName); err != nil {
			return c, err
		}
		chats = append(chats, ch)
		if contactName.Valid {
			contactNames = append(contactNames, contactName.String)
		} else {
			contactNames = append(contactNames, "")
		}
	}

	if err := rows.Err(); err != nil {
		return c, err
	}

	// Build display names after closing rows to avoid deadlock
	var names []string
	for i := range chats {
		displayName := chats[i].Name
		if displayName == "" {
			displayName = contactNames[i]
		}
		if displayName == "" {
			displayName = s.ContactName(chats[i].JID)
		}
		chats[i].Name = displayName
		names = append(names, displayName)
	}

	switch {
	case len(chats) == 0:
		return c, fmt.Errorf("chat not found: %s", ref)
	case len(chats) > 1 && !strings.EqualFold(names[0], ref):
		return c, fmt.Errorf("ambiguous %q: %s", ref, strings.Join(names, ", "))
	}

	return chats[0], nil
}

func (s *Store) GetMessage(chatJID, id string) (Message, bool, error) {
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages WHERE chat_jid=? AND id=?`, chatJID, id)
	if err != nil {
		return Message{}, false, err
	}
	ms, err := s.scanMessages(rows)
	if err != nil || len(ms) == 0 {
		return Message{}, false, err
	}
	return ms[0], true, nil
}

func (s *Store) OldestMessage(chatJID string) (Message, bool, error) {
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages WHERE chat_jid=? ORDER BY ts ASC LIMIT 1`, chatJID)
	if err != nil {
		return Message{}, false, err
	}
	ms, err := s.scanMessages(rows)
	if err != nil || len(ms) == 0 {
		return Message{}, false, err
	}
	return ms[0], true, nil
}

func (s *Store) Stats() (msgs, pendingJobs int64, err error) {
	if err = s.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&msgs); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT count(*) FROM gemini_jobs`).Scan(&pendingJobs)
	return
}
