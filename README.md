# nvgui

A native macOS (Apple Silicon) Neovim GUI built on [`mygo`](https://github.com/egoist/mygo) and Neovim's `ext_linegrid` + `ext_multigrid` + `ext_hlstate` UI protocol.

Neovim stays responsible for editing, splits, floats, and plugin layout; `nvgui` faithfully renders Neovim's grids onto Metal while adding native macOS capabilities that terminals cannot provide cleanly — starting with first-class Chinese and Japanese IME support.

## Requirements

- macOS on Apple Silicon (`darwin/arm64`)
- Neovim `0.12.x` (`nvim` in `PATH`)
- Go `1.27+` (for building from source)

## Build & Run

```bash
go build -o nvgui ./cmd/nvgui
./nvgui                                   # start or focus primary instance
./nvgui path/to/file.go                   # open in front window via safe :drop (single-instance)
./nvgui --new-window path/to/file.go      # force a new app window (independent nvim --embed)
./nvgui --no-single-instance --clean -n   # bypass single-instance IPC for isolated sessions
./nvgui --doctor                          # print PATH, nvim, Squirrel/IME, and nvgui_* snapshot
./nvgui --doctor-json                     # machine-readable JSON diagnostics (no window, 0 nvim --embed)
```

> Note: remaining arguments are forwarded directly to `nvim --embed`. Do not insert `--` before Neovim flags like `--clean` (Neovim treats arguments after `--` as filenames).

## macOS App Shell, Animations & Ligatures

- **Multi-window app shell (`Cmd+N` / `Cmd+W` / `Cmd-\``)**: one `nvgui` process manages multiple native macOS windows, each owning an isolated `nvim --embed` session with zero shared `Screen` state. Closing one window closes only its own `nvim`; the app exits cleanly once the last window or `nvim` exits.
- **Unified file handoff (CLI, Finder Open With, drag-and-drop, single-instance socket)**: routes through `vim.g.nvgui_open_in` (`"front"` default or `"new"`) and `--new-window` using Ex-injection-safe `:drop` without leaving stray empty `[No Name]` buffers. Non-terminal launches (Dock / Finder / Spotlight) probe and cache your login shell `PATH` with a bounded timeout so Homebrew/`nvim` work without blocking startup.
- **Cross-DPI & fullscreen/Space resilience**: moving between Retina/non-Retina displays rebuilds font metrics off-lock and re-layouts the grid idempotently; fullscreen toggles, Space switches, and abrupt window-manager resizes snap in-flight animations, force a full repaint, and fill sub-cell canvas margins with the root background color to prevent blank frames or white edges.
- **Restrained cursor & non-overlapping seamless scroll tape**:
  - Cursor defaults to crisp micro-elastic motion (`80ms` long jump / `30ms` short jump / `trail 0.25`, hard-clamped to `<= 1.5 * maxCell`), snaps instantly in insert mode and during rapid `j`/`k`/`h`/`l` key repeat, and tracks `cursorline` pixel-for-pixel during active scrolling.
  - Smooth scrolling (`180ms`) advances springs continuously across both 120Hz `Step` frames and incoming `Submit` redraws, retaining a deep (`max(64, innerHeight+4)`) non-overlapping multi-snapshot scroll tape so sustained `<C-d>`/`<C-u>` or held `j`/`k` never expose blank top/bottom edges while keeping floating window borders pixel-stable (`win_viewport_margins`).
  - Settled frames match animations-off output identically (`screen.State` is never mutated), and `AnimationFrame()` stops completely at rest (`idle_frames_delta = 0` over 5s idle with `MYGO_FRAME_STATS=all`).
- **Bidirectional live `vim.g.nvgui_*` hot sync**: environment variables override startup globals; at runtime, `:let g:nvgui_* = ...` applies live without blocking `-- More --` / `Press ENTER` prompts, and Go-side updates sync back to `vim.g.nvgui_*` (`g:nvgui = 1`).

| Feature | Default | Environment Variable | Live Neovim Global (`vim.g.*`) |
|---|---|---|---|
| Cursor spring animation (`80ms` / `30ms` short jump, `trail 0.25`) | On (`1`) | `NVGUI_CURSOR_ANIM=0\|1` | `nvgui_cursor_animation` / `nvgui_cursor_animation_length` |
| Animate cursor in insert mode | Off (`0`) | `NVGUI_CURSOR_ANIMATE_INSERT=0\|1` | `nvgui_cursor_animate_insert_mode` |
| Animate cursor into/out of command line | Off (`0`) | `NVGUI_CURSOR_ANIMATE_COMMAND_LINE=0\|1` | `nvgui_cursor_animate_command_line` |
| Seamless smooth scrolling (`180ms`, non-overlapping tape) | On (`1`) | `NVGUI_SCROLL_ANIM=0\|1` | `nvgui_scroll_animation` / `nvgui_scroll_animation_length` |
| Disable all animations (keeps ligatures) | Off (`0`) | `NVGUI_NO_ANIMATIONS=1` | — |
| OpenType programming ligatures (`calt`/`liga`/`clig`/`dlig`) | On (`1`) | `NVGUI_LIGATURES=0\|1` | `nvgui_ligatures` |
| File handoff target (`"front"` or `"new"`) | `"front"` | `NVGUI_OPEN_IN=front\|new` | `nvgui_open_in` |
| Single-instance Unix socket path | User temp dir | `NVGUI_SOCKET=/path/to/sock` (or `--socket`) | — |
| Override startup `guifont` (live `:set guifont=` preserved) | Unset | `NVGUI_GUIFONT="JetBrainsMono Nerd Font:h16"` | `:set guifont=...` |

## Architecture

Modules follow a strict top-down dependency direction:

- **Pure Go core** (headless, zero UI/GPU dependency): `connection` → `protocol` → `screen` → `diag` / `session` / `drawlist` / `renderlist` / `oracle` / `fuzzgen`
- **macOS UI & rendering**: `text` (`guifont` + CJK/emoji/Nerd Font fallback + Core Text OpenType ligatures + shaping cache), `render` (contiguous ASCII run merging + two-layer content glyph cache + spring cursor/scroll animator + box-drawing/Powerline geometry), `input` (macOS keymap + IME state machine + FIFO async queue + Squirrel config helper), `app` & `cmd/nvgui`

## Public macOS IME Acceptance Matrix

Tested against **Neovim v0.12.6** on **macOS 26.6.2 (25G83)** inside an isolated Apple Silicon VM using physical `CGEvent` keycodes only (no `CGEventKeyboardSetUnicodeString`, TIS selected before window creation with readback verification, buffer oracle via `nvim_buf_get_lines()` + `nvim_get_mode()`):

| Input Method | TIS Source ID | L2 VM Status | Verified Behavior & Regression Guards | Headless Trace Replay & Diag Bundle |
|---|---|---|---|---|
| **macOS Pinyin – Simplified (SCIM)** | `com.apple.inputmethod.SCIM.ITABC` | **PASS** | `nihao` → candidate window → `Space` commit `你好`; full-width punctuation `，。！？` in insert (`neovide#2567`); compose `Backspace` typo fix (`neovide#2813`); `Esc`/`Ctrl-C`/`Ctrl-[` normal-mode protection & `:`/`j`/`k` (`neovide#2723`, incl. full-width punctuation normalization in non-IME modes); `:vsplit` right-pane candidate window tracks cursor (`win_screenpos=[1, 41]`, `neovide#1721`). Buffer: `["你好，。！？", "世界", "中文", "测试"]`, mode `n`. | 111 events / 20 sends, 100% buffer match; `diag/input-trace.jsonl` 8,131 B (`ReplayBundle` PASS, 0 stalls) |
| **macOS Japanese – Romaji (Kotoeri)** | `com.apple.inputmethod.Kotoeri.RomajiTyping.Japanese` | **PASS** | `nihongo` → conversion candidate window → physical `Return` confirm strictly keeps `len(lines) == 1` without accidental newline (`qvacua/vimr#1099`); Japanese punctuation `、` and `。`; compose `Backspace` + `Esc` cancel with zero preedit leak (`neovide#2813`); normal-mode `o`/`k`/`j` protected by `InputMethod(false)`. Buffer: `["日本語、。", "にほん"]`, mode `n`. | 77 events / 10 sends, 100% buffer match; `diag/input-trace.jsonl` 5,620 B (`ReplayBundle` PASS, 0 stalls) |
| **Squirrel / Rime (1.1.2)** | `im.rime.inputmethod.Squirrel.Hans` | **`SKIPPED_REQUIRES_HUMAN_VM_ENABLE`** | **Honest skip**: macOS 26.6.2 (25G83) headless Aqua sessions keep third-party parent source `im.rime.inputmethod.Squirrel` at `isEnabled=false` (`TISSelectInputSource` returns `OSStatus -50`). No synthetic injection used. L0/L1 `ParseSquirrelAppOptions` & `CheckSquirrelBundle` (CLI vs `.app` bundle ID mismatch warning) pass 6/6 Go tests. | L0/L1 6/6 Go tests PASS |

## Performance Methodology & Results

We separate **headless Go micro-benchmarks**, **isolated VM relative regression gates**, and **bare-metal Apple M4 Pro measurements** so virtualized numbers are never presented as native hardware performance.

### 1. Headless Go Render Micro-Benchmarks (Apple M4 Pro, `go1.27.3 darwin/arm64`)

Command: `go test -run '^$' -bench '^BenchmarkRender(Plan|Draw)4K$' -benchmem -benchtime 500ms -count 6 -p 1 ./render/...` (6-run median):

| Benchmark | Grid Size | Median Time / op | Batches / op | Bytes / op | Allocs / op | Notes |
|---|---:|---:|---:|---:|---:|---|
| `BenchmarkRenderPlan4K` | 240×70 (16,800 cells) | **2.152 ms/op** | **7,241** (**-56.9%** vs 16,800 unmerged cells) | 4.06 MB | 2,998 | Contiguous same-style ASCII run merging + 32B `rowMeta` + OpenType ligatures |
| `BenchmarkRenderDraw4K` | 160×50 (with float `Clip`) | **1.223 ms/op** | **3,491** | 30.9 KB | 200 | Grouped contiguous `p.Clip` scopes + two-layer content glyph shaping cache |

### 2. Bare-Metal Apple M4 Pro (`Mac16,7`, macOS `26.6.2`)

Shared fixture (`fixture-2000.go`, 2,000 lines, `Menlo:h14`, `nvim --clean -n`), `MYGO_FRAME_STATS=all`, combined GUI app + child `nvim` CPU/RSS:

| Metric | Target | `nvgui` | Status |
|---|---|---|---|
| External `4K / 144Hz` fullscreen scroll active frame | `p95 ≤ 6.9 ms` | **`p50 4.90 / p95 5.70 / p99 8.50 ms`** (`active=1,107`, `batches p50=556`) | **PASS** |
| Built-in `3456×2234 / 120Hz` fullscreen scroll active frame | `p95 ≤ 8.3 ms` | **`p50 5.20 / p95 11.00 / p99 12.50 ms`** (`active=915`, `batches p50=449`, 2× reproducible) | **Known Gap** *(~15% ProMotion frames drop to E-core at ~2.05× slower `build+paint` with ~410 `text_layouts`/frame; tracked for follow-up)* |
| Cold start to first frame (3-run median) | Reference | **`596.51 ms`** (`596.51 / 600.40 / 584.87 ms`) | Measured |
| Idle 5s CPU / RSS (app + child `nvim`) | Reference | **`0.27% CPU`** (`app 0.27%` + `nvim 0.0%`) / **`139.77 MB`** | Measured |

### 3. Isolated VM Relative Regression Gate (**Not Native Performance**)

> **Disclaimer**: `nvim-gui-vm` runs on Apple `Virtualization.framework` with a virtualized GPU. Absolute VM numbers **must not** be read as native M4 Pro latency; they are tracked solely as a same-environment relative regression gate.

Typometer synchronous single-key protocol (`t0` → single physical `CGEvent` → concurrent Neovim state oracle + post-`present()` non-unchanged `MYGO_FRAME_STATS=all` completion; 20 warmup keys discarded, 120 valid samples = 80 `j`/`k` scroll + 40 insert; `120×40`, `Menlo:h14`, 1,000-line fixture, `nvim --clean -n`):

| Metric (VM Relative Gate) | M1 VM Baseline | M3 VM Result | Relative Change |
|---|---:|---:|---:|
| Key-to-present p50 | 63.14 ms | **41.13 ms** | **-34.9%** |
| Key-to-present p95 | 131.40 ms | **58.79 ms** | **-55.3%** |
| Key-to-present p99 | 135.19 ms | **62.69 ms** | **-53.6%** |
| Cold start to first frame (3-run median) | — | **248.02 ms** | 3 runs: `395.1 / 237.03 / 248.02 ms` |
| Idle 5s CPU / RSS median | — | **0.62% CPU** (`idle_frames_delta = 0`) / **305.41 MB** | 0 stray `--embed` nvim |

## Neovim Distribution Compatibility Matrix

Tested offline with locked commits and isolated `NVIM_APPNAME` / `XDG_*` directories against **Neovim v0.12.6** (**36/36 PASS**, 0 stalls, 121 locked plugins with 0 drift; GPL-3.0 distributions run strictly as isolated external test fixtures with zero code copied into this MIT repository):

| Distro | License | Manager (Locked Plugins) | Locked Commit(s) | S0 Boot | S1 Glyphs | S2 Extmarks | S3 Floats | S4 Noice/Cmd | S5 Image (Degrade) | S6 Statusline | S7 Terminal (Mock) | S8 Stress |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **kickstart.nvim** | MIT | `vim.pack` (19) | `8e35dbedb7` | PASS (1031ms) | PASS (612ms) | PASS (708ms) | PASS (670ms) | PASS (654ms) | PASS (636ms) | PASS (844ms) | PASS (839ms) | PASS (784ms) |
| **LazyVim** | Apache-2.0 | `lazy.nvim` (32) | `5c26fac21f` + `803bc181d7` | PASS (1178ms) | PASS (645ms) | PASS (795ms) | PASS (641ms) | PASS (713ms) | PASS (1646ms) | PASS (1018ms) | PASS (844ms) | PASS (803ms) |
| **AstroNvim** | GPL-3.0-only *(isolated fixture)* | `lazy.nvim` (43) | `fc95d5d331` + `49a7161b77` | PASS (1049ms) | PASS (618ms) | PASS (690ms) | PASS (667ms) | PASS (653ms) | PASS (1658ms) | PASS (1291ms) | PASS (841ms) | PASS (857ms) |
| **NvChad** | GPL-3.0-only *(isolated fixture)* | `lazy.nvim` (27) | `87d6a41ec0` + `e3572e1f5e` | PASS (959ms) | PASS (547ms) | PASS (671ms) | PASS (629ms) | PASS (643ms) | PASS (608ms) | PASS (973ms) | PASS (796ms) | PASS (750ms) |

## Verification & Fuzzing

- **Protocol & Grid Oracles**: Frozen golden suite (O1), frozen Neovim `screen.lua` replay oracle (O2), `nvim__inspect_cell` spot checks (O3), and PTY TUI oracle (O4, 5/5 scenarios 1920/1920 cells exact match).
- **Seeded Fuzzer & Shrinker (`fuzzgen`)**: 20 fixed seeds × 30 steps = 600 ops / 1,133 frames 1:1 exact match against frozen O2 `replay.lua` (0 divergences, 0 stalls), with deterministic ddmin shrinking.
- **Prior-Bug Regression Suite**: **52** `TestPriorBug_*` regression tests (0 skips) covering **49** P0 issues (M3 + M4 animations/ligatures + M2 macOS app shell) plus **7** animation/ligature/DPI/PATH guard issues from the Neovim GUI prior-bug corpus.

## License & Third-Party Credits

MIT License — see [LICENSE](./LICENSE) and [THIRD_PARTY.md](./THIRD_PARTY.md).
