# Third-party materials

| 来源仓库@commit | 文件 | 许可 | 我方文件 |
| --- | --- | --- | --- |
| `refs/neovim/neovim@779c36969e31080503be2dfccbcc3f999e289606` | `test/functional/ui/{multigrid,float,multibyte,hlstate,screen_basic}_spec.lua` | Apache-2.0 | `oracle/scenarios/*.lua`（M0.2 借用操作序列） |
| `refs/neovim/neovim@779c36969e31080503be2dfccbcc3f999e289606` | `test/functional/ui/screen.lua` | Apache-2.0 | `oracle/neovim-test/test/functional/ui/screen.lua`（原样引入） |
| `refs/neovim/neovim@779c36969e31080503be2dfccbcc3f999e289606` | `test/client/rpc_stream.lua`, `test/client/session.lua`, `test/client/uv_stream.lua` | Apache-2.0 | `oracle/neovim-test/test/client/*.lua`（原样引入） |

`neovim v0.12.6` 的 test 树按 Apache-2.0 登记，已由 M0.2 用 `git archive` 原样引入。`v0.12.6` 的 annotated tag object 是 `022946c802832f938398dfae34b3f277ca9509d4`，解引用后的 commit 是 `779c36969e31080503be2dfccbcc3f999e289606`；本表采用 commit hash。`test/functional/ui/screen.lua` sha256 为 `1e187bfcccf623eb31b8cef2e990e5a4df4f89ca52b7d2024669bbd02af76f74`。
