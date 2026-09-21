package store

import (
	"database/sql"
	"errors"
)

type Job struct {
	ID       int64
	ChatJID  string
	MsgID    string
	Attempts int
}

func (s *Store) EnqueueJob(chatJID, msgID string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO gemini_jobs(chat_jid, msg_id) VALUES (?,?)`, chatJID, msgID)
	return err
}

func (s *Store) NextJob(now int64) (Job, bool, error) {
	var j Job
	err := s.db.QueryRow(`SELECT id, chat_jid, msg_id, attempts FROM gemini_jobs WHERE next_at<=? ORDER BY next_at, id LIMIT 1`, now).
		Scan(&j.ID, &j.ChatJID, &j.MsgID, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return j, false, nil
	}
	return j, err == nil, err
}

func (s *Store) CompleteJob(id int64) error {
	_, err := s.db.Exec(`DELETE FROM gemini_jobs WHERE id=?`, id)
	return err
}

// FailJob updates a job's retry backoff or marks it as failed.
// Backoff is 30s * 2^attempts. Returns gaveUp=true if maxAttempts reached.
func (s *Store) FailJob(id, now int64, errMsg string, maxAttempts int) (bool, error) {
	var j Job
	if err := s.db.QueryRow(`SELECT id, chat_jid, msg_id, attempts FROM gemini_jobs WHERE id=?`, id).Scan(&j.ID, &j.ChatJID, &j.MsgID, &j.Attempts); err != nil {
		return false, err
	}

	j.Attempts++
	if j.Attempts >= maxAttempts {
		// Mark as failed and delete job
		if _, err := s.db.Exec(`UPDATE messages SET transcript_status=? WHERE chat_jid=? AND id=?`, "failed", j.ChatJID, j.MsgID); err != nil {
			return false, err
		}
		if _, err := s.db.Exec(`DELETE FROM gemini_jobs WHERE id=?`, id); err != nil {
			return false, err
		}
		return true, nil
	}

	// Calculate backoff: 30s * 2^attempts
	backoffSeconds := int64(30) << uint(j.Attempts-1)
	nextAt := now + backoffSeconds

	// Update job with new attempts and next_at
	_, err := s.db.Exec(`UPDATE gemini_jobs SET attempts=?, next_at=?, last_error=? WHERE id=?`, j.Attempts, nextAt, errMsg, id)
	return false, err
}
