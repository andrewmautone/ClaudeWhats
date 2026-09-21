package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/andrewmautone/claudewhats/internal/memory"
	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	noSpawn       bool
	noMemory      bool
	storeOverride *store.Store
	out           io.Writer = os.Stdout
)

func init() {
	root.PersistentFlags().BoolVar(&noSpawn, "no-spawn", false, "não acorda o daemon em background")
	root.PersistentFlags().BoolVar(&noMemory, "no-memory", false, "não inclui o bloco de memória")
}

// memoryStore opens (creating if needed) the memory directory. It never
// opens a store when the directory can't be created: the error must surface.
func memoryStore(cfg *config.Config) (*memory.Store, error) {
	s, _, err := memory.Create(memory.DefaultDir())
	if err != nil {
		return nil, err
	}
	return s, nil
}

// memoryBlock returns the lines of the "## memória" block for chat, or nil
// when there is nothing to show (or --no-memory). One RecallLines call,
// matching the chat's jid or contact name, case-insensitively.
func memoryBlock(s *memory.Store, chat store.Chat) []string {
	if noMemory || s == nil {
		return nil
	}
	pattern := regexp.QuoteMeta(chat.JID)
	if chat.Name != "" {
		pattern += "|" + regexp.QuoteMeta(chat.Name)
	}
	lines, err := s.RecallLines(pattern, 20)
	if err != nil || len(lines) == 0 {
		return nil
	}
	return lines
}

func openStore() (*config.Config, *store.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if storeOverride != nil {
		return cfg, storeOverride, nil
	}
	s, err := store.Open(cfg.DBPath)
	return cfg, s, err
}

func closeStore(s *store.Store) {
	if s != storeOverride {
		s.Close()
	}
}

func kick(cfg *config.Config) {
	if !noSpawn && storeOverride == nil {
		daemon.Kick(cfg)
	}
}

// parseSince accepts 24h / 7d / 30m / YYYY-MM-DD / "" (=0).
func parseSince(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.Unix(), nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("since inválido: %q", s)
		}
		return time.Now().Add(-time.Duration(n) * 24 * time.Hour).Unix(), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("since inválido: %q (use 24h, 7d, 30m ou 2026-09-21)", s)
	}
	return time.Now().Add(-d).Unix(), nil
}

func fmtTS(ts int64) string { return time.Unix(ts, 0).Local().Format("2006-01-02 15:04") }

func printf(format string, a ...any) { fmt.Fprintf(out, format, a...) }

// runWith executes the CLI against an injected store, capturing stdout (tests).
func runWith(s *store.Store, args ...string) (string, error) {
	var buf bytes.Buffer
	prev, prevStore := out, storeOverride
	out, storeOverride = &buf, s
	defer func() { out, storeOverride = prev, prevStore }()
	jsonOut, noSpawn = false, false
	resetFlags()
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// resetFlags restores every command's flags to their default value and
// clears Changed, since cobra does not reset flag state between Execute
// calls in the same process.
func resetFlags() {
	resetCmdFlags(root)
}

func resetCmdFlags(cmd *cobra.Command) {
	reset := func(f *pflag.Flag) {
		f.Value.Set(f.DefValue)
		f.Changed = false
	}
	cmd.Flags().VisitAll(reset)
	cmd.PersistentFlags().VisitAll(reset)
	for _, c := range cmd.Commands() {
		resetCmdFlags(c)
	}
}
