# OpenCode 2 终端报告可靠投递

本文说明 worker report 如何可靠进入 OpenCode 2 的原生终端会话。报告调度和会话边界见 [架构说明](../architecture.md)，终端进程的启动与退休见 [本地 Orchestrator 替换与进程生命周期](本地Orchestrator替换与进程生命周期.md)。

## 目标与非目标

**保留 OpenCode 2 的终端交互和原生对话，使 worker report 自动送达并触发后续协调。** 成功的含义是 OpenCode 已持久接纳该批报告，不要求模型已经读完或完成审查。模型执行失败与消息没有送达是不同事实。

**不扩展 TUI 与 Chat 的切换能力。** OpenCode 2 的 ACP 驱动注册、原生会话迁移和终端报告接收是三项独立能力，不能通过启用 Chat 菜单绕过本问题。报告状态的新 UI、历史报告清理、其它 harness 的投递方式也不在本方案范围内。

## 根因与核心方案

**原有失败发生在终端写入之前。** `session_manager.Manager.SendSemantic` 要求精确消息回执；OpenCode 2 的 `EmitsSemanticMessageAcceptance` 为 false，因此报告一直保留在 outbox 中重试。普通会话消息可以送进终端，但一次成功的 PTY 写入不足以证明报告已被接纳。

**由终端进程中的 AO 插件调用原生 `session.prompt`，daemon 通过本地 IPC 取得持久接纳结果。** 插件已经运行在目标 OpenCode 的私有服务中，直接使用该实例的 `context.session`，无需寻找共享服务、另起 ACP controller 或读取 OpenCode 数据库。

**要求 OpenCode 2.3.0 或更新的兼容 2.x 版本。** 官方发布的 `@opencode/client`、`@opencode/plugin` 与 `@opencode/schema` 2.3.0 声明了 `SessionPromptInput.id`、`delivery: "queue"` 和返回的 `SessionInboxUser`。运行时仍校验能力和返回身份；版本号不能代替实际接纳结果。接口说明见 [官方服务插件文档](https://opencode.ai/docs/build/plugins/server)。

## 结构与依赖边界

**report coordinator 继续拥有 outbox 和调度。** `service/report.Coordinator` 仍负责批次 ID、claim、重试和 acknowledge；完成报告的五分钟合并窗口保持原义。只有语义投递成功返回后才能 acknowledge，避免把“已保存报告”误报为“目标已收到”。

**session manager 负责身份和生命周期准入。** `SendSemantic` 优先使用可选的 `ports.AgentTerminalMessageSender`，其它终端继续使用现有 hook 回执。原生投递持有 session input lease，并在准入后重读 session，防止 Kill、Restore 或替换把报告送进另一代 controller。

**OpenCode 2 adapter 负责本地协议。** `adapters/agent/opencodev2` 拥有桥接地址、请求超时、回执校验和平台差异。它通过 `AgentRuntimeLaunchEnv` 传入本代桥接位置；CLI、renderer 和 report service 不接触 provider 的私有 API。

**已管理的 `assets/ao-activity.ts` 同时承载活动观察和报告桥接。** 活动 hook 仍是有序、尽力而为的观察，不承担持久投递确认。报告桥接只调用当前进程的原生会话 API，不能将原生数据库当作写入入口。

## 接纳与重试

**使用 `queue` 接纳报告，不自动打断当前模型回合。** 空闲会话可开始处理，忙碌会话由原生队列安排后续处理。桥接不粘贴文本、不发送 Enter 或 Ctrl-C，也不修改用户的未发送草稿；等待权限决定的 session 仍拒绝自动投递。

**消息 ID 由原生会话 ID 和 report 批次 ID 确定。** ID 使用 OpenCode 要求的 `msg_` 前缀，并保持跨 daemon 重启和同一原生会话恢复稳定。不得将 runtime launch ID 加入消息 ID，否则一次回执丢失后的恢复会生成重复消息。

**回执必须对应本次原生会话与消息 ID，且条目类型为 user。** 连接成功、字节发送成功、pre-admission hook 或无关的 active 事件都不是送达证据。原生 API 返回错误、身份不符、连接中断或超时，均保留 outbox 的待投递状态。

**同一个 ID 的重试交给原生接纳机制去重。** 回执可能在 OpenCode 持久接纳后、AO acknowledge 前丢失；因此重试沿用原 ID，不能通过“先发送、再记本地成功文件”制造第二套不原子的接纳记录。

**报告批次内容沿用现有 AO report envelope。** 批次身份用于接纳去重，envelope 用于保留报告来源；内部报告不得被提升成用户亲自输入的对话事实。

## 本地桥接与身份隔离

**桥接只使用 Unix domain socket 或 Windows named pipe。** 不新增 TCP/LAN 监听，不连接 OpenCode 的共享默认服务，不改变 AO 的 HTTP listener。这样报告目标由 AO 拥有的终端进程确定，而非由机器上恰好运行的服务决定。

**桥接描述文件和 Unix socket 位于本次 `AO_DATA_DIR` 下。** 文件记录本代端点和随机令牌，目录及文件使用私有权限；Windows pipe 名由数据目录和本代身份派生，避免不同实验环境碰撞。长路径导致 IPC 无法创建时必须报错，不能把 AO 状态回退写到系统默认 app-data 或任意临时目录。

**每次请求绑定 AO session、runtime launch 和原生 session。** 插件校验令牌及 AO 代际，目标必须是当前插件观察到的根会话；子代理的事件不得接管目标。session manager 还要求原生身份已由本代活动 hook 确认，防止恢复失败后向旧对话投递。

**网络与消息读取都有上限和取消边界。** 桥接请求及回执按帧限制长度；请求受上下文和固定超时约束。取消只能撤销尚未完成的接纳，已持久接纳的消息不能被本地取消假定为未送达，仍须以相同 ID 重试核实。

**插件释放时先清理自己发布的描述文件，再释放桥接地址。** 清理前核对令牌，并在清理期间保持地址占用；后来者只有绑定成功后才能发布，旧插件不会在检查令牌之后误删新端点。失效描述文件也不能作为当前代的回执。daemon 重启可重新使用仍存活的同代桥接，不接管 provider 进程所有权。

## 兼容与失败行为

**旧终端进程没有桥接时，报告继续待投递并留下明确错误。** 新插件必须在一次正常启动或恢复时加载；daemon 不为投递报告擅自重启用户正在工作的终端，也不在失败后偷偷退回 PTY 输入。

**原生 API 不可用或回执不符合契约时明确拒绝。** 不通过放开 `EmitsSemanticMessageAcceptance` 掩盖兼容性问题；该标志仍然描述现有活动 hook，不能声称 pre-admission 观察具备持久接纳保证。

**历史 pending 报告保持现有路由和调度语义。** 本修复不删除、伪造 acknowledge 或根据 worker 已终止而静默丢弃报告；是否清理过时协调信息属于独立的用户操作与产品决策。

## 被否决的方案

**只显示投递错误被否决。** 它改善诊断，却无法完成 worker 到 orchestrator 的自动协调。

**直接启用 hook 接收能力被否决。** OpenCode 的 prompt hook 在持久接纳之前执行，准备失败时仍可能已经观察到 hook；据此 acknowledge 会丢报告。

**以普通 `Send` 成功作为报告回执被否决。** PTY 可能只接受粘贴，Enter 未提交或终端处于其它界面；后台重试还可能重复提交同一报告。

**通过共享服务发现、另起 Chat 或写原生 SQLite 投递被否决。** 这些方式分别可能选错实例、产生第二个 controller 或绕过原生调度和去重事务，破坏现有终端对话的所有权。

## 未实现的部分与扩展准入

报告送达状态 UI、旧报告的显式撤销、其它 harness 的原生投递以及 OpenCode 的 TUI/Chat 迁移均暂缓。扩展原生投递必须证明同会话接纳、跨回执丢失去重和 controller 代际隔离；仅有活动事件或成功的本地写入不构成准入依据。
