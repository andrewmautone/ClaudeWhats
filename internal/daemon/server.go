package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/gemini"
	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/andrewmautone/claudewhats/internal/wa"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type Sender interface {
	SendText(ctx context.Context, to types.JID, text string) (string, error)
	RequestHistory(ctx context.Context, chat types.JID, oldest *types.MessageInfo, count int) error
	IsConnected() bool
}

type StatusResponse struct {
	OK          bool  `json:"ok"`
	Connected   bool  `json:"connected"`
	Messages    int64 `json:"messages"`
	PendingJobs int64 `json:"pending_jobs"`
	PID         int   `json:"pid"`
}

type Server struct {
	Store    *store.Store
	WA       Sender
	Idle     time.Duration
	Log      *log.Logger
	Shutdown func()

	mu       sync.Mutex
	lastSeen time.Time
}

// Touch renews the idle timer.
func (s *Server) Touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

// IdleLoop calls Shutdown once Idle has elapsed without a Touch. Idle<=0 means never.
func (s *Server) IdleLoop(ctx context.Context) {
	if s.Idle <= 0 {
		<-ctx.Done()
		return
	}
	s.Touch()
	tick := time.NewTicker(s.Idle / 4)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.mu.Lock()
			idle := time.Since(s.lastSeen)
			s.mu.Unlock()
			if idle >= s.Idle {
				if s.Log != nil {
					s.Log.Printf("idle %s, encerrando", idle.Round(time.Second))
				}
				s.Shutdown()
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		s.Touch()
		msgs, jobs, _ := s.Store.Stats()
		writeJSON(w, 200, StatusResponse{OK: true, Connected: s.WA != nil && s.WA.IsConnected(), Messages: msgs, PendingJobs: jobs, PID: os.Getpid()})
	})
	mux.HandleFunc("POST /send", func(w http.ResponseWriter, r *http.Request) {
		s.Touch()
		var req struct{ Chat, Text string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Chat == "" || req.Text == "" {
			writeJSON(w, 400, map[string]string{"error": "chat e text são obrigatórios"})
			return
		}
		jid, err := types.ParseJID(req.Chat)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		id, err := s.WA.SendText(r.Context(), jid, req.Text)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"id": id})
	})
	mux.HandleFunc("POST /sync", func(w http.ResponseWriter, r *http.Request) {
		s.Touch()
		var req struct {
			Chat  string
			Count int
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Chat == "" {
			writeJSON(w, 400, map[string]string{"error": "chat é obrigatório"})
			return
		}
		if req.Count <= 0 {
			req.Count = 50
		}
		jid, err := types.ParseJID(req.Chat)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		var oldest *types.MessageInfo
		if m, ok, _ := s.Store.OldestMessage(req.Chat); ok {
			sender, _ := types.ParseJID(m.SenderJID)
			oldest = &types.MessageInfo{ID: m.ID, Timestamp: time.Unix(m.TS, 0), MessageSource: types.MessageSource{Chat: jid, Sender: sender, IsFromMe: m.FromMe, IsGroup: jid.Server == types.GroupServer}}
		}
		if err := s.WA.RequestHistory(r.Context(), jid, oldest, req.Count); err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]int{"requested": req.Count})
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
		go s.Shutdown()
	})
	return mux
}

type waConn struct{ *wa.Client }

func (c waConn) IsConnected() bool { return c.WA.IsConnected() }

// Run wires store, whatsmeow, ingester, worker and HTTP; blocks until ctx is done or shutdown.
func Run(ctx context.Context, cfg *config.Config, background bool, showQR func(string)) error {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	if background {
		f, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		logger = log.New(f, "", log.LstdFlags)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("porta %d ocupada (outro daemon rodando?): %w", cfg.Port, err)
	}
	defer ln.Close()
	os.WriteFile(cfg.PidPath, []byte(strconv.Itoa(os.Getpid())), 0o600)
	defer os.Remove(cfg.PidPath)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	client, err := wa.Open(ctx, cfg.DBPath, waLog.Stdout("WA", "WARN", false))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stopOnce sync.Once
	shutdown := func() { stopOnce.Do(cancel) }

	ing := &Ingester{Store: st, MediaDir: cfg.MediaDir, DL: client, Log: logger, OnLoggedOutFn: shutdown}
	if err := client.Connect(ctx, ing, showQR); err != nil {
		if errors.Is(err, wa.ErrNotPaired) {
			logger.Print(err)
		}
		return err
	}
	defer client.Disconnect()
	logger.Printf("conectado, pid %d, porta %d", os.Getpid(), cfg.Port)

	worker := &Worker{Store: st, AI: gemini.New(cfg.GeminiAPIKey, cfg.GeminiModel), Log: logger}
	go worker.Run(ctx)

	srv := &Server{Store: st, WA: waConn{client}, Log: logger, Shutdown: shutdown}
	if background {
		srv.Idle = cfg.IdleTimeout
	}
	go srv.IdleLoop(ctx)
	hs := &http.Server{Handler: srv.Handler()}
	go hs.Serve(ln)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-sig:
	case <-ctx.Done():
	}
	logger.Print("encerrando")
	hs.Close()
	return nil
}
