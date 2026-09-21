package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Contact struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Notes string   `json:"notes,omitempty"`
	Auto  bool     `json:"auto"`
	JIDs  []string `json:"jids"`
}

func KindOf(jid string) string {
	if strings.HasSuffix(jid, "@lid") {
		return "lid"
	}
	return "pn"
}

func userPart(jid string) string {
	if i := strings.IndexByte(jid, '@'); i > 0 {
		return jid[:i]
	}
	return jid
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Store) contactOf(jid string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT contact_id FROM identities WHERE jid=?`, jid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) newContact(name string, auto bool) (int64, error) {
	a := 0
	if auto {
		a = 1
	}
	res, err := s.db.Exec(`INSERT INTO contacts(name, auto, created_at) VALUES (?,?,?)`, name, a, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) putIdentity(jid string, contactID int64, pushName string) error {
	_, err := s.db.Exec(`INSERT INTO identities(jid, contact_id, kind, push_name) VALUES (?,?,?,?)
		ON CONFLICT(jid) DO UPDATE SET contact_id=excluded.contact_id,
		push_name=CASE WHEN excluded.push_name<>'' THEN excluded.push_name ELSE identities.push_name END`,
		jid, contactID, KindOf(jid), pushName)
	return err
}

// merge moves every identity of `from` into `into` and deletes `from`.
func (s *Store) merge(into, from int64) error {
	if into == from {
		return nil
	}
	if _, err := s.db.Exec(`UPDATE identities SET contact_id=? WHERE contact_id=?`, into, from); err != nil {
		return err
	}
	// keep a name if the winner has none
	_, err := s.db.Exec(`UPDATE contacts SET name=(SELECT name FROM contacts WHERE id=?) WHERE id=? AND name=''`, from, into)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM contacts WHERE id=?`, from)
	return err
}

// winner picks which of two contacts survives a merge: manual (auto=0) wins, else lowest id.
func (s *Store) winner(a, b int64) (into, from int64, err error) {
	var autoA, autoB int
	if err = s.db.QueryRow(`SELECT auto FROM contacts WHERE id=?`, a).Scan(&autoA); err != nil {
		return
	}
	if err = s.db.QueryRow(`SELECT auto FROM contacts WHERE id=?`, b).Scan(&autoB); err != nil {
		return
	}
	switch {
	case autoA == 0 && autoB != 0:
		return a, b, nil
	case autoB == 0 && autoA != 0:
		return b, a, nil
	case a < b:
		return a, b, nil
	default:
		return b, a, nil
	}
}

// ResolveIdentity returns the contact for jid, creating an auto contact when unknown.
// altJID (the LID<->PN alternative) is bound to the same contact.
func (s *Store) ResolveIdentity(jid, altJID, pushName string) (int64, error) {
	if jid == "" {
		return 0, errors.New("empty jid")
	}
	id, err := s.contactOf(jid)
	if err != nil {
		return 0, err
	}
	var altID int64
	if altJID != "" && altJID != jid {
		if altID, err = s.contactOf(altJID); err != nil {
			return 0, err
		}
	}
	// Create new auto contact if jid is unknown
	if id == 0 {
		id, err = s.newContact(pushName, true)
		if err != nil {
			return 0, err
		}
	}
	// Put identity for jid
	if err := s.putIdentity(jid, id, pushName); err != nil {
		return 0, err
	}
	// Merge if altJID exists and points to different contact
	if altID != 0 && altID != id {
		into, from, err := s.winner(id, altID)
		if err != nil {
			return 0, err
		}
		if err := s.merge(into, from); err != nil {
			return 0, err
		}
		id = into
	}
	// Put identity for altJID if provided
	if altJID != "" && altJID != jid {
		if err := s.putIdentity(altJID, id, pushName); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func (s *Store) AddContact(name, number string) (int64, error) {
	d := digits(number)
	if d == "" {
		return 0, fmt.Errorf("número inválido: %q", number)
	}
	jid := d + "@s.whatsapp.net"
	existing, err := s.contactOf(jid)
	if err != nil {
		return 0, err
	}
	if existing != 0 {
		_, err = s.db.Exec(`UPDATE contacts SET name=?, auto=0 WHERE id=?`, name, existing)
		return existing, err
	}
	id, err := s.newContact(name, false)
	if err != nil {
		return 0, err
	}
	return id, s.putIdentity(jid, id, "")
}

// findContact resolves a jid, phone number or (case-insensitive substring) name to a contact id.
func (s *Store) findContact(ref string) (int64, error) {
	if strings.Contains(ref, "@") {
		id, err := s.contactOf(ref)
		if err != nil {
			return 0, err
		}
		if id == 0 {
			return 0, fmt.Errorf("jid desconhecido: %s", ref)
		}
		return id, nil
	}
	if d := digits(ref); d != "" && d == strings.TrimPrefix(ref, "+") {
		return s.findContact(d + "@s.whatsapp.net")
	}
	rows, err := s.db.Query(`SELECT id, name FROM contacts WHERE lower(name) LIKE '%'||lower(?)||'%' ORDER BY (lower(name)=lower(?)) DESC, id LIMIT 5`, ref, ref)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []int64
	var names []string
	for rows.Next() {
		var id int64
		var n string
		rows.Scan(&id, &n)
		ids = append(ids, id)
		names = append(names, n)
	}
	switch {
	case len(ids) == 0:
		return 0, fmt.Errorf("contato não encontrado: %s", ref)
	case len(ids) > 1 && !strings.EqualFold(names[0], ref):
		return 0, fmt.Errorf("ambíguo %q: %s", ref, strings.Join(names, ", "))
	}
	return ids[0], nil
}

func (s *Store) LinkContacts(a, b string) (int64, error) {
	ia, err := s.findContact(a)
	if err != nil {
		return 0, err
	}
	ib, err := s.findContact(b)
	if err != nil {
		return 0, err
	}
	into, from, err := s.winner(ia, ib)
	if err != nil {
		return 0, err
	}
	return into, s.merge(into, from)
}

func (s *Store) RenameContact(ref, name string) error {
	id, err := s.findContact(ref)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE contacts SET name=?, auto=0 WHERE id=?`, name, id)
	return err
}

func (s *Store) ListContacts(q string) ([]Contact, error) {
	rows, err := s.db.Query(`SELECT c.id, c.name, c.notes, c.auto, group_concat(i.jid, ' ')
		FROM contacts c LEFT JOIN identities i ON i.contact_id=c.id
		WHERE ?='' OR lower(c.name) LIKE '%'||lower(?)||'%' OR i.jid LIKE '%'||?||'%'
		GROUP BY c.id ORDER BY c.name, c.id`, q, q, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		var auto int
		var jids sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.Notes, &auto, &jids); err != nil {
			return nil, err
		}
		c.Auto = auto == 1
		if jids.Valid {
			c.JIDs = strings.Fields(jids.String)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ContactName is the display name for a jid: contact name, else push name, else jid user.
func (s *Store) ContactName(jid string) string {
	var name, push string
	err := s.db.QueryRow(`SELECT c.name, i.push_name FROM identities i JOIN contacts c ON c.id=i.contact_id WHERE i.jid=?`, jid).Scan(&name, &push)
	if err == nil {
		if name != "" {
			return name
		}
		if push != "" {
			return push
		}
	}
	return userPart(jid)
}
