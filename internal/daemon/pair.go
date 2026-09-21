package daemon

import (
	"bytes"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	qrcode "github.com/skip2/go-qrcode"
)

// pairState is what /status reports about pairing: whether the session is paired,
// whether the daemon is currently emitting QR codes, when the last one was written,
// and where the QR files live.
type pairState struct {
	pngPath, txtPath string

	mu        sync.Mutex
	paired    bool
	pairing   bool
	updatedAt time.Time
}

func (p *pairState) setPairing(on bool) {
	p.mu.Lock()
	p.pairing = on
	if !on {
		p.updatedAt = time.Time{}
	}
	p.mu.Unlock()
}

func (p *pairState) setPaired() {
	p.mu.Lock()
	p.paired, p.pairing, p.updatedAt = true, false, time.Time{}
	p.mu.Unlock()
}

func (p *pairState) touch(t time.Time) {
	p.mu.Lock()
	p.updatedAt = t
	p.mu.Unlock()
}

func (p *pairState) snapshot() (pairing bool, updatedAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pairing, p.updatedAt
}

func (p *pairState) isPaired() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paired
}

// qrSink receives each QR code from wa.Connect: it writes qr.png and qr.txt (for
// `claudewhats pair` / the skill) and, in foreground, also prints it via show.
type qrSink struct {
	pair *pairState
	show func(code string)
	log  interface{ Printf(string, ...any) }
}

func (q *qrSink) onQR(code string) {
	q.pair.setPairing(true)
	var txt bytes.Buffer
	qrterminal.GenerateHalfBlock(code, qrterminal.L, &txt)
	if err := writeAtomic(q.pair.txtPath, txt.Bytes()); err != nil {
		q.logf("gravar %s: %v", q.pair.txtPath, err)
	}
	png, err := qrcode.Encode(code, qrcode.Medium, 512)
	if err == nil {
		err = writeAtomic(q.pair.pngPath, png)
	}
	if err != nil {
		q.logf("gravar %s: %v", q.pair.pngPath, err)
	}
	q.pair.touch(time.Now())
	if q.show != nil {
		q.show(code)
	}
}

// clear removes the QR files and leaves the pairing state; called once paired.
func (q *qrSink) clear() {
	for _, p := range []string{q.pair.pngPath, q.pair.txtPath} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			q.logf("remover %s: %v", p, err)
		}
	}
	q.pair.setPairing(false)
}

func (q *qrSink) logf(format string, a ...any) {
	if q.log != nil {
		q.log.Printf(format, a...)
	}
}

// writeAtomic writes to path via a temp file + rename so readers never see a
// partially written QR.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
