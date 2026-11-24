# Third-party materials

| 来源仓库@commit | 文件 | 许可 | 我方文件 |
| --- | --- | --- | --- |
| `refs/neovim/neovim@779c36969e31080503be2dfccbcc3f999e289606` | `test/functional/ui/{multigrid,float,multibyte,hlstate,screen_basic}_spec.lua` | Apache-2.0 | `oracle/scenarios/*.lua`（M0.2 借用操作序列） |
| `refs/neovim/neovim@779c36969e31080503be2dfccbcc3f999e289606` | `test/functional/ui/screen.lua` | Apache-2.0 | `oracle/neovim-test/test/functional/ui/screen.lua`（原样引入） |
| `refs/neovim/neovim@779c36969e31080503be2dfccbcc3f999e289606` | `test/client/rpc_stream.lua`, `test/client/session.lua`, `test/client/uv_stream.lua` | Apache-2.0 | `oracle/neovim-test/test/client/*.lua`（原样引入） |
| `refs/neovim/go-client@7088209f9f86aaeab1c226118859f218488b5e53` | `nvim/nvim.go:695` `decodeExt` | Apache-2.0 | `protocol/protocol.go`（M0.3 照抄 MsgPack ext 解码规则） |
| `refs/neovim/go-client@7088209f9f86aaeab1c226118859f218488b5e53` | `nvim/nvim.go:213-260` `NewChildProcess` 管道接法 | Apache-2.0 | `connection/connection.go`（M0.3 照抄 stdin/stdout 管道接法） |
| `refs/egoist/mygo@400ba3b17c3ec60ef9b0604e26aa4cdcf2a2ff2d` | `LICENSE`、公开 `mygo` / `mygo/ui` API | MIT | `go.mod` 固定 `github.com/egoist/mygo v0.10.1-0.20261118142248-400ba3b17c3e`（M1 渲染、字体、输入与应用壳） |
| `refs/egoist/mygo@400ba3b17c3ec60ef9b0604e26aa4cdcf2a2ff2d` | `plugins/terminal/box.go` | MIT | `render/box.go`（框线、Block、Sextant 与 Powerline 自绘逻辑） |
| `refs/ghostty-org/ghostty@befcdfd2c3a1cb24d9ec886e93c95b2b5daa7028`（经 mygo `box.go` 继承） | box-arm / quadrant 映射表 | MIT | `render/box.go`（`boxArms` 与 `blockQuadrants` 查表） |
| `refs/neovide/neovide@c331febc97a8f9ac432991f3780076ed02625577` | `src/renderer/animation_utils.rs:67-116` | MIT | `render/spring.go`（临界阻尼弹簧解析公式） |
| `refs/neovide/neovide@c331febc97a8f9ac432991f3780076ed02625577` | `src/renderer/cursor_renderer/mod.rs:65-215,465-598` | MIT | `render/cursor_anim.go`（四角独立弹簧光标、长短跳与方向对齐拖尾、cmdline/insert 开关与像素对齐） |
| `refs/neovide/neovide@c331febc97a8f9ac432991f3780076ed02625577` | `src/renderer/rendered_window.rs:267-300,866-955`、`src/bridge/events.rs:969-997` | MIT | `protocol/viewport.go`、`screen/viewport.go`、`render/scroll_anim.go`（`win_viewport` `scroll_delta` / `win_viewport_margins` 解析与内视口平滑滚动） |
| `refs/qvacua/vimr@390580113fce110600219125170b087104a443f5` | `Commons/Sources/Commons/ProcessUtils.swift:10-80`、`VimR/VimR/AppDelegate.swift:342-357` | MIT | `app/path_probe.go`、`app/handoff.go`（非终端启动登录 shell PATH 探测与 Finder Open With / 拖放打开策略参考） |
| `refs/neovide/neovide@c331febc97a8f9ac432991f3780076ed02625577` | `src/bridge/ui_commands.rs:96-165` | MIT | `app/handoff.go`（冷启动前 Open With / 拖放文件排队至 UI attach 就绪再派发的队列模式） |
| `refs/neovide/neovide@c331febc97a8f9ac432991f3780076ed02625577` | `src/bridge/mod.rs:276-280`、`src/window/mouse_manager.rs:37-59,201-246,262-355,417-555`、`src/editor/mod.rs:290-296`、`src/bridge/ui_commands.rs:312-342,417-449` | MIT | `protocol/viewport.go`、`screen/viewport.go`、`input/mouse.go`、`input/queue.go`、`app/mouse.go`（Neovim 0.12 `grid=0` 全局命中、按钮名映射、跨格去重拖拽与 `mousemoveevent`、`hasMoved` 松手防抖、`mouse_on`/`mouse_off`、浮点格滚动累加器与相邻同向滚轮队列合并及顺序屏障） |
| `refs/neovide/neovide@c331febc97a8f9ac432991f3780076ed02625577` | `src/renderer/mod.rs:213-214,236-237,596-608`、`src/bridge/events.rs:578-582,1118`、`src/editor/mod.rs:259-265`、`src/settings/window_size.rs:79-83` | MIT | `text/types.go`、`text/resolver.go`、`protocol/viewport.go`、`screen/viewport.go`、`app/display.go`、`app/shell.go`、`app/workspace.go`、`app/nvim_sync.go`（`os_scale_factor` 与 `user_scale_factor` 正交分离、`set_title` 解码与空串回退 `"nvgui"`、仅主窗记忆窗口位置/尺寸设计） |

`neovim v0.12.6` 的 test 树按 Apache-2.0 登记，已由 M0.2 用 `git archive` 原样引入。`v0.12.6` 的 annotated tag object 是 `022946c802832f938398dfae34b3f277ca9509d4`，解引用后的 commit 是 `779c36969e31080503be2dfccbcc3f999e289606`；本表采用 commit hash。`test/functional/ui/screen.lua` sha256 为 `1e187bfcccf623eb31b8cef2e990e5a4df4f89ca52b7d2024669bbd02af76f74`。
