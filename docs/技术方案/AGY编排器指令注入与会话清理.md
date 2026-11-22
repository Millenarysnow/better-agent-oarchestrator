# AGY 编排器指令注入与会话清理

本文记录 AGY Terminal UI 会话如何接收 AO 的角色指令与 CLI 技能，以及 harness 切换时如何避免这些 AGY 专属配置影响后续会话。AO 通用 session 生命周期见 [architecture.md](../architecture.md)，固定角色指令的来源见 [Worker 与 Orchestrator 固定规则文档](Worker与Orchestrator固定规则文档.md)。

## 目标与非目标

**AGY 作为 AO worker 或 orchestrator 启动时，必须收到本 session 的完整 AO system prompt，并能发现 `using-ao` 技能。** 否则 AGY 可能不知道 orchestrator 必须通过 `ao spawn` 委派工作，也可能不知道该 CLI 的准确用法。

**注入只属于当前 AGY session。** 不把运行时生成的角色规则写入项目 `AGENTS.md`、`.agents/rules/` 或用户级 `~/.gemini/` 配置；这些位置会跨 harness 或跨项目生效，导致切换到 Codex 等 harness 后重复或遗留指令。

**不改变 AO 的通用角色提示词、其他 harness 的注入路径或 AGY Chat 能力。** AO 仍由 `session_manager` 生成角色规则，AGY adapter 只负责把它转换为 AGY 原生配置。当前 fork 的 `agyacp` 已通过独立 ACP server 和首轮 standing-instructions preamble 服务 Chat；本方案不更换它的 transport，也不让 AGY 使用其内建 subagent 承担 AO worker 的职责。

## 核心方案

**用 AGY workspace custom agent 承载 AO system prompt，用 workspace skill 暴露 `using-ao`，并随 AGY controller 生命周期创建和清理。** AGY CLI 原生发现 workspace 下的 `.agents/agents/` 和 `.agents/skills/`，而 Claude Code 的 `--append-system-prompt-file` 参数不能用于 AGY。参见 [AGY customization discovery](https://antigravity.google/docs/customization-discovery?tab=cli) 与 [AGY custom agents](https://antigravity.google/docs/subagents?tab=cli)。

AGY adapter 在当前 session workspace 中生成 AO 管理的 primary custom agent，并由 `agy --agent <name>` 选择它。agent 正文使用 AO 传入的 `SystemPrompt`，只将其中 `using-ao` 文件根路径改为 workspace 内的副本；用户初始任务仍通过 `--prompt-interactive` 作为任务消息发送。custom agent 禁止作为 AGY 内建 subagent 使用，worker 必须由 AO session 管理器创建。

同一 session 的 restore 重新物化同一 custom agent，并在恢复命令中再次选择它。这样不依赖 AGY native conversation 是否保留启动时的 active custom agent，也不需要把完整规则重复塞入恢复时的用户消息。

## 结构与依赖边界

**`session_manager` 继续拥有 system prompt 的语义；`backend/internal/adapters/agent/agy` 只负责 AGY 的文件格式、启动参数和清理。** AO 已在启动和恢复时把 `SystemPrompt`、`SystemPromptFile` 传给 adapter；不增加 API、项目配置项或 renderer 逻辑。

**AGY 的配置只写入当前 AO workspace。** `.agents/agents/` 中的 primary custom agent 与 `.agents/skills/using-ao/` 中的技能由 AGY adapter 创建。系统级 AGY 配置、AO data-dir 之外的全局技能目录和项目永久规则文件不作为 session 注入目标；否则 AGY 指令会超出 AO session 的生命周期。

**生成物必须具有明确的 AO 所有权标记。** 安装时遇到没有 AO 标记的同名 agent 或 skill，启动应失败并保留用户文件；卸载时只删除 AO 所有的 agent、skill 和 hook 项。清理失败必须返回错误，不能让 AO 报告成功切换后继续留下可被其他 harness 读取的 AO 规则。

**安装文件必须保持 Git worktree 可回收。** AO 生成的 custom agent、技能副本、ownership marker 和 AGY hook 配置需由局部 `.gitignore` 排除。用户已有的 `.gitignore` 或 `.agents` 内容不得被覆盖。

## Harness 切换与恢复

**从 AGY 切换到其他 harness 时，在 AGY controller 停止后清理 AGY session 配置。** 清理必须发生在目标 harness 成为最终运行 controller 之前；否则目标 agent 可能扫描到 AGY 专用技能或规则。清理失败时恢复源 AGY session，并将切换报告为失败。

**切换目标启动失败时，恢复流程重建源 harness 所需文件。** 若源 harness 是 AGY，现有 session restore 会重新调用 AGY workspace preparation，重新生成同一份 custom agent 和 skill；若 AGY 是失败的目标，则先清除目标生成物再恢复原 harness。

**daemon restore 与新 session 使用相同的 preparation 路径。** 因为工作区是 AO session 的一部分，prompt 文件或 workspace agent 文件不能只在首次 spawn 时创建；否则进程重启后恢复的 AGY 会话可能没有 AO 角色规则。

**共享 workspace preparation 必须知道本次 controller 是 Terminal UI 还是 Chat。** `WorkspaceHookConfig.SessionMode` 区分这两条指令通道；Chat preparation 只保留 activity hooks，并清理此前 TUI 使用的 custom agent 和 skill。TUI→Chat handoff 在 AGY storage bridge 完成会话复制后执行同样的指令清理，因为普通 Chat resume 不会重新安装 workspace hooks。否则同一 workspace 切到 Chat 后会保留另一条指令通道。

## 必须守住的约束与陷阱

**不要把 AO 完整 system prompt 写入常驻 `AGENTS.md`、GEMINI 规则或全局 AGY custom agent。** 这些来源会随 workspace 或用户配置跨 harness 继续生效，正是切换后重复注入的来源。

**不要把 AO system prompt 附加到 `--prompt-interactive`。** 它是首条用户任务，不具备常驻 system 指令语义；restore 或换 provider 时也可能被重放成普通任务内容。

**AGY custom agent 与 AGY skill 是不同用途。** custom agent 放 session 的角色 system prompt；`using-ao` skill 保存具体 CLI 工作流。两者都需 workspace scope，且都须在 AGY session 退出其运行期或切换离开时清理。

## 被否决的方案

**复用 Claude Code 的 `--append-system-prompt-file` 被否决。** 这是 Claude CLI 的原生参数，不构成 AGY adapter 的通用接口；不支持的 flag 会直接导致 AGY 启动失败。

**把所有内容并入用户首条任务被否决。** 这会混淆角色规则与用户任务，并在恢复、继续和换 harness 时产生重复或丢失。

**把 AGY 指令写入项目常驻规则文件被否决。** 同一 worktree 在 TUI provider switch 中会被复用，Codex 等后继 harness 可能再次读取这些规则，违背 session/harness 隔离目标。

## 未实现的部分与扩展准入

该方案只新增 AGY Terminal UI 的指令通道。既有 AGY ACP transport 的更新、runtime subagent 代理、跨 AGY native conversation 的自定义 agent 语义，以及 AGY workspace custom agent 格式变化的兼容检测均不在范围内。扩展前必须确认 AGY 原生接口仍能将 AO 指令设为主 agent system prompt，并确保 profile 不跨 AO session 或 harness 切换泄漏。
