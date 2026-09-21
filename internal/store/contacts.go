package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
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

// Match tiers for name lookups, best first: the query equals the name once
// emoji, punctuation and spaces are stripped; a word of the name starts with
// the query (the first word included); the query is a plain substring.
const (
	matchNone = iota
	matchSubstring
	matchWordPrefix
	matchExact
)

// alnum lowercases s and keeps only letters and digits, so "Amor☀️💛" and
// "amor" compare equal.
func alnum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchTier ranks how well name answers the query q.
func matchTier(name, q string) int {
	lq := strings.ToLower(strings.TrimSpace(q))
	ln := strings.ToLower(name)
	if lq == "" || ln == "" {
		return matchNone
	}
	if aq := alnum(q); aq != "" && aq == alnum(name) {
		return matchExact
	}
	for _, w := range strings.FieldsFunc(ln, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.HasPrefix(w, lq) {
			return matchWordPrefix
		}
	}
	if strings.Contains(ln, lq) {
		return matchSubstring
	}
	return matchNone
}

// bestMatches keeps the indexes of the candidates in the top tier, where each
// candidate's tier is the best of its names[i] (a chat may match by chat name,
// contact name or push name). Candidates with no match at all are dropped.
func bestMatches(q string, names [][]string) []int {
	best := matchNone
	tiers := make([]int, len(names))
	for i, ns := range names {
		for _, n := range ns {
			if t := matchTier(n, q); t > tiers[i] {
				tiers[i] = t
			}
		}
		if tiers[i] > best {
			best = tiers[i]
		}
	}
	if best == matchNone {
		return nil
	}
	var out []int
	for i, t := range tiers {
		if t == best {
			out = append(out, i)
		}
	}
	return out
}

// execer is what *sql.DB and *sql.Tx have in common; the identity helpers take
// it so ResolveIdentity/LinkContacts can run them inside one transaction.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// begin opens a write transaction. The DSN carries _txlock=immediate, so the
// write lock is taken up front and concurrent resolvers serialize instead of
// both seeing "unknown" and creating duplicate contacts.
func (s *Store) begin() (*sql.Tx, error) {
	return s.db.Begin()
}

func contactOf(q execer, jid string) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT contact_id FROM identities WHERE jid=?`, jid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func newContact(q execer, name string, auto bool) (int64, error) {
	a := 0
	if auto {
		a = 1
	}
	res, err := q.Exec(`INSERT INTO contacts(name, auto, created_at) VALUES (?,?,?)`, name, a, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func putIdentity(q execer, jid string, contactID int64, pushName string) error {
	_, err := q.Exec(`INSERT INTO identities(jid, contact_id, kind, push_name) VALUES (?,?,?,?)
		ON CONFLICT(jid) DO UPDATE SET contact_id=excluded.contact_id,
		push_name=CASE WHEN excluded.push_name<>'' THEN excluded.push_name ELSE identities.push_name END`,
		jid, contactID, KindOf(jid), pushName)
	return err
}

// merge moves every identity of `from` into `into` and deletes `from`.
func merge(q execer, into, from int64) error {
	if into == from {
		return nil
	}
	if _, err := q.Exec(`UPDATE identities SET contact_id=? WHERE contact_id=?`, into, from); err != nil {
		return err
	}
	// keep a name if the winner has none
	_, err := q.Exec(`UPDATE contacts SET name=(SELECT name FROM contacts WHERE id=?) WHERE id=? AND name=''`, from, into)
	if err != nil {
		return err
	}
	_, err = q.Exec(`DELETE FROM contacts WHERE id=?`, from)
	return err
}

// winner picks which of two contacts survives a merge: manual (auto=0) wins, else lowest id.
func winner(q execer, a, b int64) (into, from int64, err error) {
	var autoA, autoB int
	if err = q.QueryRow(`SELECT auto FROM contacts WHERE id=?`, a).Scan(&autoA); err != nil {
		return
	}
	if err = q.QueryRow(`SELECT auto FROM contacts WHERE id=?`, b).Scan(&autoB); err != nil {
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
	tx, err := s.begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := contactOf(tx, jid)
	if err != nil {
		return 0, err
	}
	var altID int64
	if altJID != "" && altJID != jid {
		if altID, err = contactOf(tx, altJID); err != nil {
			return 0, err
		}
	}
	// Determine which contact to use before putIdentity
	switch {
	case id == 0 && altID == 0:
		// Neither jid nor altJID known: create new auto contact
		id, err = newContact(tx, pushName, true)
		if err != nil {
			return 0, err
		}
	case id == 0 && altID != 0:
		// jid unknown but altJID known: reuse altJID's contact
		id = altID
	case id != 0 && altID != 0 && id != altID:
		// Both known but different: merge them
		into, from, err := winner(tx, id, altID)
		if err != nil {
			return 0, err
		}
		if err := merge(tx, into, from); err != nil {
			return 0, err
		}
		id = into
	}
	// Put identities (defer until after merge succeeds)
	if err := putIdentity(tx, jid, id, pushName); err != nil {
		return 0, err
	}
	if altJID != "" && altJID != jid {
		if err := putIdentity(tx, altJID, id, pushName); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// PNForLID returns the phone-number jid bound to the same contact as lid, if any.
func (s *Store) PNForLID(lid string) (string, bool) {
	var pn string
	err := s.db.QueryRow(`SELECT pn.jid FROM identities l JOIN identities pn ON pn.contact_id=l.contact_id
		WHERE l.jid=? AND pn.kind='pn' LIMIT 1`, lid).Scan(&pn)
	return pn, err == nil && pn != ""
}

// UnlinkedLIDs lists the LID jids whose contact has no phone-number identity.
func (s *Store) UnlinkedLIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT l.jid FROM identities l WHERE l.kind='lid'
		AND NOT EXISTS (SELECT 1 FROM identities pn WHERE pn.contact_id=l.contact_id AND pn.kind='pn')
		ORDER BY l.jid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// SetContactNameIfAuto sets the address-book name on jid's contact unless the
// user has named it manually (auto=0). Unknown jids get an auto contact first.
func (s *Store) SetContactNameIfAuto(jid, name string) error {
	if name == "" {
		return nil
	}
	id, err := s.ResolveIdentity(jid, "", "")
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE contacts SET name=? WHERE id=? AND auto=1`, name, id)
	return err
}

// SetPushNameIfEmpty fills jid's push name only when none has been seen yet.
func (s *Store) SetPushNameIfEmpty(jid, name string) error {
	if name == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE identities SET push_name=? WHERE jid=? AND push_name=''`, name, jid)
	return err
}

func (s *Store) AddContact(name, number string) (int64, error) {
	d := digits(number)
	if d == "" {
		return 0, fmt.Errorf("número inválido: %q", number)
	}
	jid := d + "@s.whatsapp.net"
	tx, err := s.begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	existing, err := contactOf(tx, jid)
	if err != nil {
		return 0, err
	}
	if existing != 0 {
		if _, err = tx.Exec(`UPDATE contacts SET name=?, auto=0 WHERE id=?`, name, existing); err != nil {
			return 0, err
		}
		return existing, tx.Commit()
	}
	id, err := newContact(tx, name, false)
	if err != nil {
		return 0, err
	}
	if err := putIdentity(tx, jid, id, ""); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// findContact resolves a jid, phone number or name to a contact id. Names match
// case-insensitively; the best tier wins (see matchTier) and a lookup is only
// ambiguous when two contacts tie in that tier.
func (s *Store) findContact(ref string) (int64, error) {
	if strings.Contains(ref, "@") {
		id, err := contactOf(s.db, ref)
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
	rows, err := s.db.Query(`SELECT id, name FROM contacts WHERE lower(name) LIKE '%'||lower(?)||'%' ORDER BY id LIMIT 50`, ref)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []int64
	var names [][]string
	for rows.Next() {
		var id int64
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			return 0, err
		}
		ids = append(ids, id)
		names = append(names, []string{n})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	best := bestMatches(ref, names)
	if len(best) == 0 {
		return 0, fmt.Errorf("contato não encontrado: %s", ref)
	}
	if len(best) > 1 {
		var ns []string
		for _, i := range best {
			ns = append(ns, names[i][0])
		}
		return 0, fmt.Errorf("ambíguo %q: %s", ref, strings.Join(ns, ", "))
	}
	return ids[best[0]], nil
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
	tx, err := s.begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	into, from, err := winner(tx, ia, ib)
	if err != nil {
		return 0, err
	}
	if err := merge(tx, into, from); err != nil {
		return 0, err
	}
	return into, tx.Commit()
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
