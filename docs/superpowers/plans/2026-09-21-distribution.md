# ClaudeWhats Distribution Plan (plugin + release)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use `- [ ]`.

**Goal:** Users install with `/plugin marketplace add andrewmautone/ClaudeWhats` + `/plugin install claudewhats@claudewhats`; `claudewhats` is then on PATH and downloads the release binary on first run.

**Architecture:** Repo root is a Claude Code plugin and its own marketplace. `bin/` holds two shims (bash + cmd) that fetch `claudewhats_<os>_<arch>[.exe]` from GitHub Releases into `~/.claudewhats/bin/` and exec it. GoReleaser + GitHub Actions publish releases on `v*` tags.

**Spec:** `docs/superpowers/specs/2026-09-21-claudewhats-design.md` (+ this plan).

## Global Constraints
- Marketplace name `claudewhats`, plugin name `claudewhats`, version `0.1.0` (bump both files together on release).
- Release asset names: `claudewhats_windows_amd64.exe`, `claudewhats_windows_arm64.exe`, `claudewhats_linux_amd64`, `claudewhats_linux_arm64`, `claudewhats_darwin_amd64`, `claudewhats_darwin_arm64`, plus `checksums.txt`. Download URL: `https://github.com/andrewmautone/ClaudeWhats/releases/latest/download/<asset>`.
- Binary install dir: `~/.claudewhats/bin/claudewhats[.exe]` (Windows: `%USERPROFILE%\.claudewhats\bin\claudewhats.exe`). Env `CLAUDEWHATS_BIN` overrides the path; `CLAUDEWHATS_VERSION=vX.Y.Z` pins the download tag (`releases/download/<tag>/<asset>`).
- Shims must be silent on the happy path (just exec), print a one-line "baixando claudewhats <asset>..." to stderr when downloading, and fail with a clear message (asset URL) if download fails. Windows shim uses `curl.exe` (present on Win10+); bash shim uses `curl` (fallback `wget`).
- `claudewhats version` prints the ldflags version (`dev` when unset).
- Commit trailer `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.

## Tasks

### Task 1: version command + goreleaser + workflow
**Files:** Create `.goreleaser.yaml`, `.github/workflows/release.yml`; Modify `internal/cli/root.go` (add `var Version = "dev"` and `version` subcommand printing `claudewhats <Version>`), `cmd/claudewhats/main.go` unchanged (ldflags target `github.com/andrewmautone/claudewhats/internal/cli.Version`).
- [ ] `.goreleaser.yaml` v2: `builds: [{id: claudewhats, main: ./cmd/claudewhats, binary: claudewhats, env: [CGO_ENABLED=0], goos: [windows, linux, darwin], goarch: [amd64, arm64], ldflags: ["-s -w -X github.com/andrewmautone/claudewhats/internal/cli.Version={{.Version}}"]}]`, `archives: [{formats: [binary], name_template: "claudewhats_{{ .Os }}_{{ .Arch }}"}]`, `checksum: {name_template: checksums.txt}`, `release: {github: {owner: andrewmautone, name: ClaudeWhats}}`, `changelog: {use: git}`.
- [ ] `.github/workflows/release.yml`: on `push: tags: ['v*']`; permissions `contents: write`; steps: checkout (fetch-depth 0), `actions/setup-go@v5` with `go-version-file: go.mod`, `goreleaser/goreleaser-action@v6` with `version: '~> v2'`, `args: release --clean`, env `GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}`.
- [ ] Test: `go test ./internal/cli/` has a case `run(t, s, "version")` containing "claudewhats dev". Validate goreleaser config locally if `go run github.com/goreleaser/goreleaser/v2@latest check` works offline; otherwise `goreleaser build --snapshot --clean --single-target` via `go run` (may download; acceptable). Commit `build: version command, goreleaser and release workflow`.

### Task 2: plugin + marketplace + shims + skill move
**Files:** Create `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `bin/claudewhats`, `bin/claudewhats.cmd`; Move `skill/SKILL.md` → `skills/claudewhats/SKILL.md` (git mv, delete `skill/`); Modify `README.md`.
- [ ] plugin.json: `{name, version: "0.1.0", description (PT-BR one line), author {name: "Andrew Mautone", url}, homepage, repository, license: "MIT", keywords}`. marketplace.json: `{name: "claudewhats", owner: {name: "Andrew Mautone"}, plugins: [{name: "claudewhats", source: ".", description, version: "0.1.0"}]}`. Add `LICENSE` (MIT, Andrew Mautone, 2026).
- [ ] `bin/claudewhats` (bash, extensionless, `chmod +x`, LF endings — add `bin/* text eol=lf` to `.gitattributes`): resolve `BIN=${CLAUDEWHATS_BIN:-$HOME/.claudewhats/bin/claudewhats}`; on MSYS/Git-Bash (`uname -s` matches MINGW*|MSYS*|CYGWIN*) os=windows and BIN gets `.exe`; arch from `uname -m` (x86_64→amd64, arm64|aarch64→arm64); if `! -x "$BIN"`: mkdir -p, echo to stderr, `curl -fsSL -o "$BIN.tmp" URL && mv "$BIN.tmp" "$BIN" && chmod +x` (fallback wget), on failure print URL and exit 1; then `exec "$BIN" "$@"`.
- [ ] `bin/claudewhats.cmd` (CRLF ok): `@echo off`, `setlocal`, `set BIN=%CLAUDEWHATS_BIN%`, default `%USERPROFILE%\.claudewhats\bin\claudewhats.exe`; arch from `%PROCESSOR_ARCHITECTURE%` (AMD64→amd64, ARM64→arm64); if not exist: mkdir, echo, `curl.exe -fsSL -o "%BIN%.tmp" URL`, on error print URL and `exit /b 1`, `move /y`; then `"%BIN%" %*` and `exit /b %ERRORLEVEL%`.
- [ ] Test the shims locally against a fake server? Not worth it: instead a bash test script `scripts/test-shim.sh` is NOT needed — verify manually in Task 3 against the real release. But do a dry test now: `CLAUDEWHATS_BIN=/tmp/x/claudewhats CLAUDEWHATS_VERSION=v0.0.0 bash bin/claudewhats status` must fail with the clear message containing the URL (no release exists yet), exit 1.
- [ ] README: replace Setup with: Instalação (plugin commands + `claudewhats serve` once + config key), "Primeiro uso", "Atualizar" (`/plugin update claudewhats` for the skill; `rm ~/.claudewhats/bin/claudewhats*` to refetch binary, or `CLAUDEWHATS_VERSION`), "Instalar sem plugin" (download asset manually or `go install ...@latest`). Keep Memória/Smoke sections.
- [ ] SKILL.md: add a "Setup" note: the `claudewhats` command comes from the plugin's `bin/` (downloads the binary on first run); if the user has never paired, tell them to run `claudewhats serve` in their own terminal. Commit `feat: claude code plugin, marketplace and release shims`.

### Task 3 (controller): publish
- [ ] `gh repo create andrewmautone/ClaudeWhats --public --source . --push`; `git tag v0.1.0 && git push --tags`; watch the Action; verify assets; `rm -rf ~/.claudewhats/bin && bash bin/claudewhats version` downloads and prints `claudewhats 0.1.0`; `/plugin marketplace add` + install smoke in a fresh session is for the human.
