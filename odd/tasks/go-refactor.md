# Go rewrite of wut

## Objective
Rewrite this fork of `wut` in Go on branch `refactor/go`, preserving the current user-facing behavior before considering feature expansion.

## Problem and rationale
The repository is a Python CLI that explains the latest command output by capturing the active tmux or GNU screen pane and sending the context to a configured LLM provider. The user chose Go and parity-first scope. A Go implementation provides a standalone CLI while keeping the existing feature contract.

## Scope
- Implement the `wut` command in Go.
- Preserve tmux and GNU screen pane capture, shell/prompt-aware command extraction, `--query`, `--debug`, configuration precedence, provider selection, and supported providers (OpenAI, Anthropic, Ollama), subject to checking current provider/API compatibility during implementation.
- Preserve the documented UX, not accidental edge-case failures: correct error handling/exit behavior and parsing robustness are allowed; do not add undocumented short flags or new input modes.
- Add behavior-focused tests and update installation/usage documentation for the Go implementation.
- Do not add stdin/file input, automatic command execution/fixing, or other new features.
- Preserve the existing Python source during the initial Go implementation unless a later explicit migration/removal decision is made.

## Constraints and observed state
- Branch created: `refactor/go` (off current `master` at `f8c4ce9`).
- Pre-existing worktree state must be preserved: modified `.gitignore`; untracked `.codegraph/`.
- Current implementation requires either `TMUX` or `STY`; it captures the pane via `tmux capture-pane` or `screen -X hardcopy` respectively. It does not currently accept arbitrary stdin.
- Existing implementation is in `wut/`; packaging metadata in `setup.py`; documentation in `README.md`; config sample in `config.example`.
- No existing tests or Go module were observed at planning time.
- TDD mode: strict TDD, explicitly selected by the user for this feature. Runner: `go test ./...` once the Go module exists.

## Tasks
- [x] T1 — Specify and implement Go CLI structure, argument handling, and terminal capture/parsing with focused tests. Completed; work-unit commit `312fa62` (`feat: add Go CLI terminal capture core`).
- [x] T2 — Implement configuration loading/provider selection and OpenAI, Anthropic, and Ollama calls with focused tests. Completed; work-unit commit `fb602d2` (`feat: add provider configuration and LLM clients`).
- [x] T3 — Complete Go user-facing output/rendering, update build/install/config docs, and verify Go is fully usable while Python files remain. Completed; commit `8bab3be` (`feat: render Markdown and document Go CLI`).
- [x] T4 — Remove Python implementation and packaging artifacts (`wut/`, `setup.py`, `Pipfile`, `Pipfile.lock`). Completed; cleanup commit `5d93d4b` (`chore: remove legacy Python implementation`).
- [x] T5 — Add Kitty scrollback capture as fallback after tmux and screen, with bounded capture, tests, and setup documentation. Completed; commit `6990e58` (`feat: add Kitty scrollback capture fallback`).

## Acceptance criteria
- A Go-built `wut` supports current documented query/debug behavior and prefers tmux, then screen; Kitty is used only when neither multiplexer is usable. If a stale mux environment points to a dead/unusable mux, capture failure may fall through to the explicitly identified Kitty window.
- Context extraction preserves the intended latest-command behavior and excludes the current `wut` invocation.
- Configuration precedence and provider selection match documented/current behavior.
- No stdin/file ingestion or auto-fix feature is added.
- Tests cover parsing, config/provider selection, and command behavior without requiring live API credentials; all claimed checks have observed evidence.
- README documents how to build/install/run the Go implementation and accurately describes the tmux/screen requirement.
- Markdown responses remain readable in a terminal; Go build/install instructions work independently of Python.
- Kitty capture uses only the current `KITTY_WINDOW_ID`, requests plain `all` scrollback for command+output parsing, and bounds retained data; missing remote-control permission fails clearly without silently reading another window or widening Kitty permissions. Successful tmux/screen capture must never invoke Kitty.

## Verification and progress
- TDD: strict, user-selected for this feature.
- Runner: `go test ./...`; Go module/toolchain availability must be confirmed by the implementation/verification workers.
- Progress: branch `refactor/go` created from `f8c4ce9`. T1 and T2 are implemented and independently verified with fresh `go test ./... -count=1`. T1 commit `312fa62` (`feat: add Go CLI terminal capture core`); T2 commit `fb602d2` (`feat: add provider configuration and LLM clients`). Strict TDD used. T2 preserves provider precedence, prompts, and request shapes; standard library only, no new dependencies. No API network credentials used. User chose documented-UX parity with edge-case fixes. Shell lookup uses portable `ps ax -o pid=,ppid=,comm=` rather than Linux-only `/proc`; macOS/BSD process behavior and GNU screen E2E remain environment-limited. Go module declares 1.22. Native risk assessment unavailable (`package-local-binary-missing`); separate independent verification passed prior tasks. Pre-existing `.gitignore` modification and `.codegraph/` remain untouched.
- T3 outcome: commit `8bab3be` (`feat: render Markdown and document Go CLI`). Independent fresh `go test ./... -count=1` passed; install/build instructions were checked statically; T4 writer additionally executed the root binary build/help path.
- T4 implementation removed the authorized tracked Python sources and packaging files and changed README to the final `go build -o wut` path. Independent verifier passed `go test ./... -count=1`, `go build ./...`, and a `/tmp` binary help smoke test. The root README build target was confirmed available, but the exact root build was not executed to avoid leaving a binary in the repo. `go install` was statically checked, not run. Incident: worker also removed ignored `wut/__pycache__/` despite the scope boundary; user was informed and explicitly chose to continue. It cannot be recovered from Git. Ignored `build/` and `wut_cli.egg-info/` remain untouched. No Go source or pre-existing `.gitignore`/`.codegraph/` was changed.
- T4 outcome: `5d93d4b` (`chore: remove legacy Python implementation`); independent tests/build/help verification passed. User accepted the accidental removal of ignored Python bytecode cache; ignored build artifacts remain.
- T5 outcome: commit `6990e58` (`feat: add Kitty scrollback capture fallback`). Independent fresh `go test ./... -count=1` passed across five packages; writer also reports gofmt. Native risk assessment remained unavailable; independent verifier and final spot-check passed. Kitty E2E remains unverified because the agent session had no controlling TTY. The new commit is local and has not been pushed.
- Original Go rewrite complete and pushed. T5 is a follow-up Kitty backend implementation on the same branch; the earlier push does not authorize pushing this new commit.

## Route and delivery
- T1-T5: delegated direct implementation uses bounded workers and separate `gentle-ai-verify` checks for command-running verification.
- Work-unit commits: explicitly authorized by the user for this feature; T1-T5 commits recorded above. Push authorization covered the earlier Go rewrite only; T5 remains unpushed pending explicit user instruction.
- Forecast: substantial rewrite. User selected separate commits for Go integration/docs and Python cleanup to keep the destructive deletion isolated; no push or PR authorized.
