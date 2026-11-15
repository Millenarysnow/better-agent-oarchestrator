# CodeBuddy CLI Harness

本文记录 AO 接入腾讯 CodeBuddy Code CLI 的设计边界。这里的 CodeBuddy 指官方 `codebuddy` 命令行工具；WorkBuddy 桌面端和 WorkBuddy Managed Agents 是不同产品接口，不属于本设计。AO 当前架构见 [architecture.md](../architecture.md)，现有 ACP 注册边界见 `backend/internal/adapters/chatdriver/registry`。

## 目标与非目标

- **让 AO 能用用户已安装、已登录的 CodeBuddy CLI 创建本地会话。** 会话运行于 AO 管理的 workspace，受 AO lifecycle 管理，并可使用 CodeBuddy 原生 CLI 会话恢复。
- **首版接入 TUI。** 使用 `codebuddy` 交互界面运行于 AO workspace，支持 prompt delivery、活动跟踪和原生会话恢复；活动信号按部分覆盖处理，不能把 hook 沉默当作失效证据。
- **ACP Chat 暂不登记为支持能力。** 官方确认 `codebuddy --acp` 存在，但没有公开 AO 所需的恢复、权限和取消能力契约；AO 的 Chat 生产门槛需要 streaming、approvals、interrupt 和 resume。仅凭启动参数不能安全地宣称 Chat 可用。
- **不控制 WorkBuddy 桌面应用，也不接入 WorkBuddy Managed Agents 云端沙箱。** 二者的运行环境和工作目录边界与 AO 本地 worktree 不同；不能把同一引擎误认为同一接入协议。
- **AO 不下载、更新或代管 CodeBuddy CLI。** 安装、登录和账号配置仍归 CodeBuddy；AO 只解析并启动用户本机的 `codebuddy`。

## 核心方案

新增 `codebuddy` harness，由 TUI adapter 负责本地进程、workspace hook、活动和 native session restore。CodeBuddy 官方文档公开了交互式 CLI、hook 和 `--resume`；AO 不另建 HTTP 服务，也不把单轮 `-p` 或未经能力验证的 `--acp` 模式当作会话 runtime。

## 结构与依赖边界

- **领域与注册：** `domain.AgentHarness` 和 worker registry 声明 `codebuddy`；Chat driver registry 单独声明其 ACP Chat 能力。两套 registry 表达不同能力，增加 worker 不会自动意味着支持 Chat。
- **TUI adapter：** `backend/internal/adapters/agent/codebuddy/` 复用 binary resolver 和 `hooksjson.Manager`。workspace-local `.codebuddy/settings.local.json` 只合并 AO 管理的 hook，保留用户设置；hook 经 `ao hooks codebuddy` 报告活动并读取 AO 已存放于 `AO_DATA_DIR` 的 standing instructions。
- **Chat 准入：** 不向 Chat registry 注册 `codebuddy`。ACP Chat 以后复用 `nativeacp` 与 `chatdriver/acp`，但必须先实测目标版本的 ACP handshake、permission enforcement、cancel 和 session resume，再决定暴露哪些能力。
- **产品识别：** API harness enum、SQLite sessions 约束和前端 Agent identity/catalog 必须包含同一个 `codebuddy` ID；OpenAPI 和 TypeScript schema 仍从现有生成流程更新。

## 行为约定

- **binary 不存在时报告未安装，不启动空终端。** 只解析官方 CLI 名称及其常见包管理器路径，不猜测 WorkBuddy 私有安装路径。
- **TUI 采用官方交互命令及显式参数启动。** 初始任务仍由 AO 在终端 ready 后送入，避免用户任务以 `-` 开头时被 CLI 误认为参数。模型与 AO permission mode 映射到 CodeBuddy 文档确认的选项；未选择模型时使用 CLI 自身配置。
- **只信任本次 AO 选定的 workspace。** 通过进程级 `--settings` overlay 提供该目录的 `trustedDirectories`，避免初始任务误送入目录信任对话框；不设置 `trustAll`，也不写入用户全局信任配置。工具权限仍由 CodeBuddy 自己执行，AO 的 hook 不返回授权决定；`bypassPermissions` 也不绕过供应商保留的高风险确认。
- **TUI restore 只按 hook 捕获的 CodeBuddy 原生 session ID 调用 `--resume`。** 没有可验证的原生 ID 时按 AO 既有 restore 语义新开会话；禁止用 AO session ID 冒充供应商 session ID。
- **local auth probe 不能证明模型端可用。** 文档未提供一个可依赖的本机认证检查命令时，readiness 保持 unknown；不把凭据文件/变量存在等同于授权成功，也不因未知而阻止 TUI 启动。
- **Hook 是 best-effort lifecycle signal。** Hook 失败不得中断 CodeBuddy；无 hook 信号不能证明进程已退出，reaper 仍按现有 runtime 规则处理存活状态。
- **`SessionStart` 只更新身份，`SessionEnd` 不映射为进程退出。** 恢复、压缩和清空也会触发原生会话生命周期；把这些事件当作任务活跃或终端结束会误导 AO。真正退出由 AO 的 runtime supervisor 观察。

## 不变量与反直觉的坑

- **只由 adapter 解析和启动本机 CLI。** 以后接入 Chat 时也必须复用同一 binary resolver；另找或下载一个 `codebuddy` 会使 TUI 与 Chat 使用不同登录、版本和会话存储。
- **AO 写入的 hook 配置必须留在 workspace 或 AO_DATA_DIR。** 不得修改用户全局 `~/.codebuddy` 设置来安装 hook，否则卸载 session 时会污染用户配置，并造成多个 AO session 互相覆盖。
- **CodeBuddy 的项目设置优先级高于用户设置。** AO hook 需落在项目级 local settings，并以 `ao hooks codebuddy ` 命令前缀识别和合并/卸载；替换整个 settings 文件会静默移除用户 permissions、MCP 和 hooks。
- **新增 SQLite harness 迁移不能破坏历史约束修复。** `reconcileHarnessConstraint` 仍有按完整历史字符串匹配的修复；CodeBuddy 迁移会先扩展这些旧约束，因此修复期间需暂时移除、最后恢复 `codebuddy`，否则旧数据库会因早期 harness 缺失而无法启动。已有 CodeBuddy 行时必须拒绝回滚，避免留下违反旧 CHECK 的数据。
- **WorkBuddy 与 CodeBuddy 共享引擎不构成登录复用保证。** 不读取 WorkBuddy 私有凭据、不从其配置目录推断 CodeBuddy CLI 身份；用户需要通过 `codebuddy` 自身支持的登录方式建立 CLI 认证。
- **ACP 支持不能由 CLI 参数表推断。** `--acp` 证明存在传输入口，不证明 CodeBuddy 所装版本已实现 AO 所需的 session resume、permission modes 或 cancellation；没有这些证据时不能注册 Chat driver。

## 被否决方案

- **直接驱动 WorkBuddy 桌面 UI。** 仓库 harness 管理本地 agent 进程和 AO worktree；桌面 UI 自动化没有稳定的 session、权限和恢复协议，会绕过 AO 的 lifecycle 边界。
- **改用 `codebuddy -p` 实现 TUI session。** print 模式是单轮后退出，不能承担 AO 的多轮终端会话、持续活动和可恢复 native conversation。
- **用 WorkBuddy Managed Agents API 伪装成本地 harness。** 其任务在云端独立沙箱执行，不保证修改 AO 的本地 worktree，因此结果无法沿用本地 Git/PR lifecycle。

## 未实现部分与扩展准入

首版只承诺用户安装的 CodeBuddy Code CLI TUI，不做自动安装、账号登录 UI、ACP Chat、WorkBuddy 桌面控制或云端 Managed Agents。Hook payload 与 Windows binary shim 行为须以目标 CLI 版本的本机验证结果为准。增加 ACP Chat 的准入条件是通过 live handshake 验证恢复、权限请求/决策、取消及实际 prompt/model config 行为，并据此设置 AO capability；不得把协议存在等同于功能等价。若上游改变 hook 或 ACP 合约，更新对应 adapter/driver 测试和本文的行为约定。

## 依据

- 腾讯云《CLI 命令参考》：[`codebuddy`、`--resume`、`--permission-mode`、`--model`、`--acp`](https://cloud.tencent.cn/document/product/1831/137026)
- 腾讯云《ACP 协议集成》：[`codebuddy --acp`](https://intl.cloud.tencent.com/zh/document/product/1316/84434)
- 腾讯云《Hooks 配置参考》：[`~/.codebuddy` / `.codebuddy` hook 结构与事件](https://cloud.tencent.com/document/product/1831/137030)
- 腾讯云《.codebuddy 目录结构》：[`CODEBUDDY_CONFIG_DIR` 与设置优先级](https://cloud.tencent.com/document/product/1831/137016)
