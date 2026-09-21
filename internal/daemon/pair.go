package daemon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
	// currentPNG is the timestamped copy of the latest QR (qr-<unix>.png): image
	// viewers cache by path, so a stable name would keep showing a stale code.
	currentPNG string
}

func (p *pairState) setPairing(on bool) {
	p.mu.Lock()
	p.pairing = on
	if !on {
		p.updatedAt, p.currentPNG = time.Time{}, ""
	}
	p.mu.Unlock()
}

func (p *pairState) setPaired() {
	p.mu.Lock()
	p.paired, p.pairing, p.updatedAt, p.currentPNG = true, false, time.Time{}, ""
	p.mu.Unlock()
}

func (p *pairState) touch(t time.Time, currentPNG string) {
	p.mu.Lock()
	p.updatedAt, p.currentPNG = t, currentPNG
	p.mu.Unlock()
}

// png returns the path /status should advertise: the timestamped file while one
// exists, otherwise the stable name.
func (p *pairState) png() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.currentPNG != "" {
		return p.currentPNG
	}
	return p.pngPath
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
	now := time.Now()
	unique := filepath.Join(filepath.Dir(q.pair.pngPath), "qr-"+strconv.FormatInt(now.Unix(), 10)+".png")
	png, err := qrcode.Encode(code, qrcode.Medium, 512)
	if err != nil {
		q.logf("gerar png: %v", err)
	} else {
		for _, p := range []string{q.pair.pngPath, unique} {
			if err := writeAtomic(p, png); err != nil {
				q.logf("gravar %s: %v", p, err)
			}
		}
		q.removeStamped(unique)
	}
	q.pair.touch(now, unique)
	if q.show != nil {
		q.show(code)
	}
}

// clear removes the QR files (stable and timestamped) and leaves the pairing
// state; called once paired.
func (q *qrSink) clear() {
	for _, p := range []string{q.pair.pngPath, q.pair.txtPath} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			q.logf("remover %s: %v", p, err)
		}
	}
	q.removeStamped("")
	q.pair.setPairing(false)
}

// removeStamped deletes every qr-*.png next to pngPath except keep.
func (q *qrSink) removeStamped(keep string) {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(q.pair.pngPath), "qr-*.png"))
	for _, m := range matches {
		if m == keep {
			continue
		}
		if err := os.Remove(m); err != nil && !errors.Is(err, os.ErrNotExist) {
			q.logf("remover %s: %v", m, err)
		}
	}
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
