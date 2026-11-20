# nvgui 便宜模型执行规则

## flash 执行规则

1. 只读本票、本票引用的文件和 `dev-loop.md` 对应兔子洞手册；不读其它票，不扩大范围。
2. 只改 `Write-scope` 内的文件；`Locked` 内一个字节都不改，包括格式化。
3. 先跑一次 `Acceptance` 看到红灯再动手；完成定义 = `Acceptance` 全绿 + `tools/check.sh <票号>` 全绿。
4. 不许按场景名、文件名、测试输入写特判；不许吞错误；不许用 `sleep` 等待状态。
5. 收尾汇报固定四段：改了哪些文件 / 跑了哪些命令与结果 / 没解决的问题 / 我怀疑契约有误的地方。

## Go 命令

所有 Go 命令必须使用前缀 `env -u GOROOT nice -n 15`，并且只运行契约指定的 Go 包。

## M1 依赖约定

- `github.com/egoist/mygo` 固定为 `v0.10.1-0.20261118142248-400ba3b17c3e`，不使用本地 `replace`。
- 只有 `text` / `render` / `input` / `app` 与 `cmd/nvgui` 可引用 `github.com/egoist/mygo` 或 `github.com/egoist/mygo/ui`；`cmd/nvgui` 属于 `app` 启动入口。
- 禁止引用 `github.com/egoist/mygo/internal/*`、`github.com/egoist/mygo/plugins/terminal` 与 mygo 未导出 API。
- `connection` / `protocol` / `screen` / `diag` / `session` / `drawlist` / `renderlist` / `oracle` 及其测试依赖均不得包含 mygo。
- 生产依赖只能向下：`app`、`cmd/nvgui` 可引用所有下层；`render` 可引用 `drawlist` / `renderlist` / `text` / `screen` / `protocol`；`input` 可引用 `screen` / `diag` / `protocol`；`diag` 可引用 `drawlist` / `renderlist` / `screen` / `protocol`（禁止引用 `input` 与 `mygo`；`TraceKind`/`TraceEvent` 定义在 `diag`，由 `input` 通过类型别名复用）；`session` 可引用 `connection` / `diag` / `protocol` / `screen`；`drawlist` 与 `text` 只引用 `screen` / `protocol`；下层不得引用上层。
- `tools/check.sh` 控制生产依赖方向、mygo 白名单与包循环；测试依赖不参与分层方向判定。
