# 飞书直连渠道接入 lark-cli 文档能力方案

本文对齐当前 CSGClaw 代码中的飞书直连渠道和 `lark-cli` 接入方式。当前实现的目标是让绑定飞书
Bot 的 Codex worker 可以在自己的运行上下文中使用 `lark-cli` 读取飞书文档，而不是让飞书
Channel Transport 直接调用文档 API。

当前实现已经落地了 worker 级 `lark-cli` 绑定、配置隔离、运行时环境注入、Feishu 评论 prompt
引导、断开飞书后的本地状态清理。尚未实现多阶段 document-tool CLI、OAuth subject guard、固定
托管安装、下载、版本锁和官方 skill 全量同步。

## 1. 当前结论

- `lark-cli` 是 Codex worker 运行时工具，不是飞书 Channel Transport 的依赖。
- 飞书 Bot 的 `app_id/app_secret` 继续放在 Feishu Agent Participant 的
  `channel_app_config`，不放入 Agent profile、prompt 或普通环境变量。
- worker profile 的 Feishu Channel 页面按检测状态提供“查看安装方法”或“配置渠道工具”按钮。
- “配置渠道工具”和安装引导中的“重新检测并配置”调用 `POST /api/v1/agents/{id}/lark-cli:init`。
- 如果宿主机已有 `lark-cli`，直接执行绑定；如果没有，则提示用户先在宿主机安装 `lark-cli`。
- 每个 worker 使用自己的 `<CODEX_HOME>/lark-cli` 作为 `LARKSUITE_CLI_CONFIG_DIR`。
- 每个 worker 使用自己的 `<CODEX_HOME>/lark-cli-source/config.json` 作为 `LARK_CHANNEL_CONFIG`。
- Runtime 只有在 source config 和 bound marker 同时存在时才注入 lark 环境变量。
- 同一宿主机多个 worker 可以同时使用同一个 `lark-cli` 二进制，但不能共用同一个 worker 配置目录。
- 同一个 Feishu AppID 当前不允许绑定给多个 worker；初始化时发现 AppID 被其他 worker 使用会拒绝。
- 重建 Codex worker 时保留该 worker 的 `lark-cli` 和 `lark-cli-source` 目录，避免已绑定的渠道工具
  退回未初始化状态。
- 断开 Feishu Agent Participant 后，会删除该 worker 的 `lark-cli` 和 `lark-cli-source` 目录。

形象地说：

```text
lark-cli 二进制                  = 楼里的公共工具箱
<worker CODEX_HOME>/lark-cli     = 这个 worker 自己的抽屉
lark-cli-source/config.json      = 这个抽屉里的飞书账号登记表
Participant channel_app_config   = CSGClaw 保存 bot app_id/app_secret 的保险柜
bound.json                       = “这个抽屉已经绑定完成”的门牌
```

## 2. lark-cli 是什么以及如何安装

`lark-cli` 对 CSGClaw 来说是一个外部可执行文件。当前代码通过 `exec.LookPath("lark-cli")` 查找它，
worker 运行时直接执行 `lark-cli ...` 命令。

当前检测逻辑位于 `internal/api/lark_cli.go`：

1. 在 `PATH` 中查找 `lark-cli`；
2. 找到则复用现有二进制并继续绑定；
3. 找不到则返回 `lark_cli_unavailable`，提示用户在宿主机安装 `lark-cli` 后重试。

因此当前代码不需要 JVM，也没有把 Node.js/npm 声明为 CSGClaw 自身依赖。`lark-cli` 由用户或部署方
安装、升级和移除；CSGClaw 自己不会执行 npm、npx、下载或全局安装动作。

Profile 页面在检测不到命令时显示“查看安装方法”，推荐在运行 CSGClaw 的账号可用的终端中执行：

```bash
npm install -g @larksuite/cli@latest
```

这条命令只安装 npm 分发的 `lark-cli` 命令，不执行 CSGClaw 的 worker 绑定，也不替 worker 执行
`config init`、OAuth 登录或全量 Skills 安装。通过 npm 安装时，宿主机需要先安装并保留 Node.js/npm，
`PATH` 中的 `lark-cli` 可能是 npm 生成的启动 shim；CSGClaw 对 shim 和原生可执行文件一视同仁，只要求
该命令能够被当前 CSGClaw 进程找到并成功执行。

没有 Node.js 的主机可以从 lark-cli 官方 Releases 下载与操作系统、CPU 架构匹配的原生二进制，将它
命名为 `lark-cli`、授予执行权限并放入 CSGClaw 进程的 `PATH`。macOS、Linux、Windows 都遵循同一
检测规则。安装后若页面仍检测不到命令，应重启 CSGClaw 以刷新进程继承的 `PATH`，然后点击
“重新检测并配置”。不建议使用 `sudo npm install -g` 绕过 npm 权限问题，应配置当前账号可写的 npm
全局目录。

这里不推荐 `npx @larksuite/cli@latest install` 作为 CSGClaw 的标准引导，因为该交互式安装器还可能
继续安装 Skills、初始化默认 profile 和引导用户 OAuth；这些动作与 CSGClaw 的 worker 私有绑定不是
同一流程。

## 3. 当前“配置渠道工具”不是 config init

页面的配置动作没有执行 `lark-cli config init`。当前实现使用的是 `lark-channel` source projection：

```bash
LARKSUITE_CLI_CONFIG_DIR=<worker CODEX_HOME>/lark-cli \
LARK_CHANNEL=1 \
LARK_CHANNEL_HOME=<worker CODEX_HOME> \
LARK_CHANNEL_PROFILE=<worker agent_id> \
LARK_CHANNEL_CONFIG=<worker CODEX_HOME>/lark-cli-source/config.json \
lark-cli config bind --source lark-channel --identity bot-only --force --lang zh
```

也就是说，配置阶段做的是“把 worker 的 Feishu App 信息绑定进这个 worker 自己的 lark-cli
配置抽屉”。App Secret 不通过命令行参数或 stdin 传给 `lark-cli`，而是由 source config 中的
exec provider 按需读取。

配置有两个入口，但复用同一个幂等后端流程：

- Codex worker 完成飞书连接时，如果宿主机 `PATH` 中存在 `lark-cli`，自动完成配置；
- Profile 的 Feishu Channel 页面保留配置入口，供安装二进制后绑定、失败重试或主动刷新绑定。

完整步骤如下：

1. 校验目标 Agent 存在且 runtime kind 为 Codex；
2. 查找该 Agent 对应的 Feishu Agent Participant；
3. 从 Participant `channel_app_config` 读取 `app_id/app_secret`；
4. 校验这个 `app_id` 没有被其他 worker 使用；
5. 解析该 worker 的 `CODEX_HOME`；
6. 准备正式配置路径 `<CODEX_HOME>/lark-cli` 和 `<CODEX_HOME>/lark-cli-source`；
7. 确认宿主机 `PATH` 中存在 `lark-cli`，找不到则返回错误提示用户自行安装；
8. 创建 staging `lark-cli` 配置目录和 staging source 目录；
9. 在 staging source 中写入 `config.json`，权限为 `0600`；
10. 对 staging 配置目录执行 `lark-cli config bind --source lark-channel --identity bot-only --force --lang zh`；
11. 在 staging source 中写入 `bound.json`，权限为 `0600`；
12. bind 成功后用 staging 目录覆盖正式 `<CODEX_HOME>/lark-cli` 和 `<CODEX_HOME>/lark-cli-source`；
13. 刷新 worker 的 managed instructions；
14. 手动配置时，如果 Codex worker 当前正在运行，则重启 worker 以加载新环境；飞书连接流程中的自动配置
    由紧随其后的渠道激活动作一次性加载，不额外重启。

正式目录不做备份。bind 失败时失败发生在 staging 中，旧的正式目录不会被改写；bind 成功后以本次
staging 结果为准直接覆盖旧目录，因此同一个 worker 可以重复配置来重试或刷新绑定。后端按 Agent ID
串行化配置，避免飞书自动配置和用户手动配置同时改写同一 worker 的目录。

## 4. Bot 信息放在哪里

Feishu Bot app 信息的事实源仍是 Feishu Agent Participant：

```json
{
  "id": "dev",
  "channel": "feishu",
  "type": "agent",
  "channel_user_kind": "app_id",
  "channel_app_config": {
    "app_id": "cli_xxxxxxxxxxxxxxxx",
    "app_secret": "[secret]"
  },
  "agent_id": "agent-dev"
}
```

`channel_app_config.app_secret` 真实落盘，但 API/CLI 的普通展示会脱敏。当前 lark-cli source
provider 需要读取真实 secret 时，会走受限的内部 API：

```text
GET /api/v1/agents/{id}/feishu/app-info
```

这个接口不接受普通 server token 作为 source token。初始化时会为该 worker 生成
`larkcli-src-v1...` 格式的 HMAC token，并写入 source config 的 exec provider 环境变量。该 token
绑定到具体 Agent ID，其他 worker 的 source token 不能读取本 worker 的 app info。

本地 helper 命令是：

```bash
pt app-info --channel feishu --agent-id <agent-id> --exec-provider
```

该命令使用 lark-cli exec secret provider 协议输出 `app_id/app_secret`。普通 CLI 输出和 JSON 输出会
对 `app_secret` 脱敏。

## 5. Worker 私有目录

当前目录结构为：

```text
<agent home>/
└── .codex/
    ├── workspace/
    └── home/                                      # CODEX_HOME
        ├── AGENTS.md
        ├── config.toml
        ├── skills/
        ├── lark-cli/                              # LARKSUITE_CLI_CONFIG_DIR, 0700
        │   └── lark-channel/
        │       └── config.json                    # lark-cli 管理
        └── lark-cli-source/                       # 0700
            ├── config.json                        # LARK_CHANNEL_CONFIG, 0600
            └── bound.json                         # CSGClaw 绑定完成标记, 0600
```

`lark-cli-source/config.json` 是给 `lark-cli config bind --source lark-channel` 读取的 source config。
它保存：

- 当前 worker 的 `app_id`；
- exec provider 命令路径；
- `pt app-info --channel feishu --agent-id <agent-id> --exec-provider` 参数；
- `CSGCLAW_BASE_URL`；
- scoped source token；
- exec provider 输出限制。

它不保存 `app_secret` 明文。

`bound.json` 是 CSGClaw 自己的 marker。Runtime 根据 `config.json` 和 `bound.json` 是否同时存在来
判断该 worker 是否已经完成 lark-cli 绑定。

## 6. Runtime 环境变量

Codex Runtime 启动 worker session 时，如果检测到：

```text
<CODEX_HOME>/lark-cli-source/config.json
<CODEX_HOME>/lark-cli-source/bound.json
```

会注入：

```text
LARKSUITE_CLI_CONFIG_DIR=<CODEX_HOME>/lark-cli
LARK_CHANNEL=1
LARK_CHANNEL_HOME=<CODEX_HOME>
LARK_CHANNEL_PROFILE=<agent_id>
LARK_CHANNEL_CONFIG=<CODEX_HOME>/lark-cli-source/config.json
```

这些变量的含义：

| 变量 | 当前含义 |
| --- | --- |
| `LARKSUITE_CLI_CONFIG_DIR` | 这个 worker 自己的 `lark-cli` 配置目录 |
| `LARK_CHANNEL` | 标记当前进程运行在 lark-channel source 上下文 |
| `LARK_CHANNEL_HOME` | 当前 worker 的 `CODEX_HOME` |
| `LARK_CHANNEL_PROFILE` | 当前 worker 的 Agent ID |
| `LARK_CHANNEL_CONFIG` | 当前 worker 的 source config 文件 |

这些 key 已加入 runtime reserved env。宿主进程继承来的同名变量会被过滤，Agent Profile 里的同名变量
也不能覆盖。这样同一个宿主机上多个 worker 同时运行时，虽然共用一个 `lark-cli` 二进制，但每个
worker 的 `lark-cli` 配置抽屉不同。

## 7. 多 worker 如何区分

多 worker 的区分点不是 `lark-cli` 二进制，而是运行时环境和配置目录。

示例：

```text
/home/.../agent-a/.codex/home/
  lark-cli/
  lark-cli-source/config.json
  lark-cli-source/bound.json

/home/.../agent-b/.codex/home/
  lark-cli/
  lark-cli-source/config.json
  lark-cli-source/bound.json
```

worker A 启动时：

```text
LARKSUITE_CLI_CONFIG_DIR=/home/.../agent-a/.codex/home/lark-cli
LARK_CHANNEL_CONFIG=/home/.../agent-a/.codex/home/lark-cli-source/config.json
LARK_CHANNEL_PROFILE=agent-a
```

worker B 启动时：

```text
LARKSUITE_CLI_CONFIG_DIR=/home/.../agent-b/.codex/home/lark-cli
LARK_CHANNEL_CONFIG=/home/.../agent-b/.codex/home/lark-cli-source/config.json
LARK_CHANNEL_PROFILE=agent-b
```

因此两个 Codex worker 可以同时使用 `lark-cli`。每次命令执行时，`lark-cli` 根据当前进程环境变量
读到不同的配置目录和 source config。

当前代码还增加了 AppID 独占校验：如果另一个 Feishu Agent Participant 已经使用同一个 `app_id`，
`POST /api/v1/agents/{id}/lark-cli:init` 会返回 `feishu_bot_app_id_conflict`。

## 8. UI 和 API

当前用户入口在 Agent profile 的 Feishu Channel 页面：

- 连接/重新连接 Feishu；
- 查看安装方法或配置渠道工具；
- 断开 Feishu。

配置动作调用：

```text
POST /api/v1/agents/{id}/lark-cli:init
```

成功响应示例：

```json
{
  "status": "configured",
  "agent_id": "agent-dev",
  "participant_id": "pt-dev",
  "app_id": "cli_xxx",
  "lark_cli_path": "/usr/local/bin/lark-cli",
  "config_dir": "<CODEX_HOME>/lark-cli",
  "config_path": "<CODEX_HOME>/lark-cli/lark-channel/config.json",
  "source_config_path": "<CODEX_HOME>/lark-cli-source/config.json",
  "restart_status": "runtime_restarted"
}
```

Agent API 会由后端读取 worker 的 `bound.json` 和 source config，并返回当前 lark-cli 状态，前端不
直接读取 worker 文件：

```json
{
  "id": "agent-dev",
  "lark_cli": {
    "bound": true,
    "available": true,
    "state": "bound",
    "executable_path": "/usr/local/bin/lark-cli",
    "app_id": "cli_xxx",
    "config_dir": "<CODEX_HOME>/lark-cli",
    "config_path": "<CODEX_HOME>/lark-cli/lark-channel/config.json",
    "source_config_path": "<CODEX_HOME>/lark-cli-source/config.json",
    "bound_at": "2026-08-27T12:00:00Z"
  }
}
```

`state=bound` 时按钮显示“已配置渠道工具”；`state=mismatch` 时显示“重新配置渠道工具”；二进制可用
但没有 marker 或 source config 时显示“配置渠道工具”。宿主机找不到二进制时返回
`state=unavailable`，按钮显示“查看安装方法”；点击只打开安装引导，不发起一个必然失败的 init 请求。
用户完成手动安装后，在引导弹窗点击“重新检测并配置”才调用同一个幂等 init API，不要求先刷新页面。
若新安装位置尚未进入 CSGClaw 进程的 `PATH`，页面提示用户重启 CSGClaw 后再检测。

飞书连接本身不依赖 lark-cli 自动配置成功。宿主机未安装或 bind 失败时，Feishu Bot 连接仍然成功，
后端记录 warning，Agent API 则返回对应的 `unavailable` 或 `unbound/mismatch` 状态供页面提示和重试。

常见错误：

| 错误码 | 含义 | UI 行为 |
| --- | --- | --- |
| `feishu_bot_not_configured` | 该 worker 没有 Feishu Bot app info | 弹窗提示先连接飞书并完成 Bot 配置 |
| `feishu_bot_app_id_conflict` | 该 AppID 已被其他 worker 使用 | 显示配置失败 |
| `lark_cli_unavailable` | 宿主机没有 `lark-cli` 或不在 `PATH` 中 | 展示安装命令、官方文档、原生 Releases 和重新检测动作 |
| `lark_cli_bind_failed` | `lark-cli config bind` 失败 | 显示配置失败；正式 source/marker 不会被改写 |
| `unsupported_runtime` | 目标不是 Codex worker | 显示配置失败 |

如果 bind 成功但 worker 重启失败，接口仍返回 `status=configured`，并带
`restart_status=restart_failed` 和 `restart_error`。前端会提示 lark-cli 已绑定，但需要手动重启
worker。

## 9. 断开 Feishu 时如何清理

删除 Feishu Agent Participant 时，当前代码会：

1. 删除同一 Agent 下其他 Feishu AppID participant；
2. 调用 `DeactivateExternalBinding` 刷新渠道侧 binding；
3. 删除该 worker 的：

```text
<CODEX_HOME>/lark-cli
<CODEX_HOME>/lark-cli-source
```

4. 刷新 managed instructions；
5. 如果 Codex worker 正在运行，则重启 worker。

这意味着用户断开飞书并选择新机器人后，旧机器人的 lark-cli 本地配置会被清掉。下一次点击
“配置渠道工具”会按新 Participant 的 `app_id/app_secret` 重新写 source config 并 bind。

## 10. 飞书评论如何使用 lark-cli

飞书评论链路仍由 Channel 负责事件、评论上下文和最终回复：

```mermaid
flowchart TD
    User["飞书用户在云文档评论中 @Bot"] --> WS["飞书 WebSocket"]
    WS --> Transport["transport.handleComment"]
    Transport --> Resolve["Wiki GetNode：解析 obj_token / obj_type"]
    Resolve --> Comment["Drive FileComment：读取 quote / reply"]
    Comment --> Ingress["ingress.prepareCommentMessage"]
    Ingress --> Engine["Agent Engine"]
    Engine --> Codex["Codex Runtime"]
    Codex --> LarkCLI["lark-cli，只在 worker 内执行"]
    Codex --> Reply["delivery.ReplyToComment"]
    Reply --> User
```

当前 `ingress.commentPrompt` 会把 file token、file type、用户选中原文和用户问题发给 Codex，并按
类型提示 worker 使用当前已绑定的 `lark-cli`：

| `file_type` | 当前 prompt 行为 |
| --- | --- |
| `doc` / `docx` | 优先提示 `lark-cli docs +fetch --api-version v2 --doc <file_token> --doc-format markdown` |
| `file` | 提示使用当前 lark-cli drive/wiki 只读下载命令，并把文件放到 `./downloads/` |
| `sheet` | 明确不要使用 `lark-cli docs +fetch`，除非当前 lark-cli 有表格只读命令 |
| 其他 | 提示使用匹配文件类型的只读命令 |

Channel 不会把 Codex 下载到 workspace 的文件自动上传回飞书。最终仍只通过评论回复文本。

## 11. Managed instructions

当 runtime 检测到 source config 和 bound marker 后，会在 worker 的 `AGENTS.md` managed block 中加入
`Feishu lark-cli Access` 指令。该指令要求：

- 普通 `lark-cli ...` 命令继承当前 worker 的 lark-channel 环境；
- 所有 lark-cli 命令必须直接通过 `command_execution` 执行，不能通过 `mcp_tool_call`、MCP 代码执行、
  Node.js/Python 子进程或其他工具包装层调用，因为这些环境可能清理 worker 的 `LARK*` 变量并错误读取
  宿主默认 profile；
- 非 `command_execution` 环境返回 `not_configured` 时不能直接判定未配置，必须通过
  `command_execution` 对同一个只读状态命令复核一次；
- 不要切到宿主默认 lark-cli profile；
- 不要读取或打印 lark-cli config、app secret、access token、refresh token、OAuth device code 或
  CSGClaw API token；
- 如果 lark-cli 提示当前上下文未绑定，则让用户在 Feishu channel profile 页面配置渠道工具或重启 worker；
- Doc/Docx 优先使用 `lark-cli docs +fetch --api-version v2 --doc <file_token> --doc-format markdown`；
- 用户提到当前飞书会话中以前上传的附件时，只使用隐藏上下文中的当前 `chat_id`，先以 Bot 身份执行
  `lark-cli im +chat-messages-list` 查询消息元数据，再按唯一匹配的 `message_id + file_key` 执行
  `lark-cli im +messages-resources-download`；
- 历史附件发现阶段不使用 `--download-resources` 批量下载，不查询其他飞书会话，不复用 CSGClaw
  内置渠道的附件目录或下载 API；
- Bot 缺少消息读取权限或不在当前会话时，报告 lark-cli 权限错误，不静默切换到用户身份；
- 需要用户 OAuth 时，只在飞书私聊中启动 `lark-cli auth login`；
- 用户 OAuth 成功后可收敛 `strict-mode` 和 `default-as`。

当前暂时没有同步所有官方 `lark-*` skills。worker 主要依赖 managed instructions 和已安装的
`lark-cli` 命令本身。

## 12. 当前安全边界

已经实现：

- App Secret 不进入 prompt；
- App Secret 不进入普通 API/CLI 展示；
- lark-cli source config 不保存 App Secret 明文；
- source token 绑定 Agent ID，不能用全局 server token 或其他 Agent source token 读取 app info；
- `LARK*` 环境变量被 runtime 保留，不能由 Agent Profile 覆盖；
- per-worker `lark-cli` 配置目录隔离；
- init 阶段拒绝同 AppID 多 worker；
- 断开 Feishu 后清理本地 lark-cli 状态。

尚未实现：

- OAuth subject 与评论 actor 的强制匹配；
- 群聊中阻止使用某个用户 OAuth Token 的服务端 guard；
- scope 状态校验和 `ready/needs_reauth` 状态机；
- CSGClaw 托管的 `lark-cli` 下载、固定版本与安装锁；
- 覆盖正式目录时跨 `lark-cli` 和 `lark-cli-source` 两个目录的完全事务原子切换；
- 对 `lark-cli` stderr 的结构化错误分类；
- 官方 lark skills 的全量同步和版本对账。

因此当前实现适合“绑定到某个 worker 的 Feishu Bot + worker 自己按需登录/读取”的本机托管场景。
如果要做企业多用户授权，需要继续实现 OAuth subject guard 或改成官方 Auth Sidecar 模式。

## 13. 当前代码落点

| 模块 | 当前职责 |
| --- | --- |
| `internal/api/lark_cli.go` | 手动/自动共用的幂等配置、per-agent 锁、查找 lark-cli、写 staging source config、执行 bind、切换正式目录、写 marker、source token |
| `internal/api/feishu_registration.go` | 飞书连接成功后对 Codex worker 尝试自动配置 lark-cli，再激活渠道 |
| `internal/api/handler.go` | Agent API 返回后端读取的 `lark_cli` status |
| `internal/api/router.go` | 注册 `/api/v1/agents/{id}/lark-cli:init` 和 `/api/v1/agents/{id}/feishu/app-info` |
| `cli/participant/app_info.go` | 提供 `pt app-info --exec-provider` 给 lark-cli source provider 调用 |
| `internal/runtime/codex/session_manager.go` | 根据 bound marker 注入并保护 `LARK*` 环境变量 |
| `internal/runtime/codex/runtime.go` | 刷新 Codex home `AGENTS.md` 时按绑定状态加入 managed instructions |
| `internal/agent/agents_instructions.go` | `Feishu lark-cli Access` managed instructions |
| `internal/api/participant.go` | 断开 Feishu participant 后清理 worker lark-cli 状态 |
| `internal/channel/feishu/ingress/comment.go` | 在评论 prompt 中提示按文件类型使用 lark-cli |
| `web/app/src/pages/AgentPage/components/AgentDetailPane/AgentDetailPane.tsx` | Feishu Channel 页面按 `lark_cli` status 展示安装引导/配置/已配置/重新配置按钮 |
| `web/app/src/hooks/workspace/useAgentController.ts` | 调用 init API，展示安装引导、成功、错误和缺少 Bot 弹窗 |

## 14. 后续最佳实践

当前代码已经能让不同 worker 在同一宿主机上用各自的 lark-cli 配置运行。后续建议按优先级补齐：

1. 增加可选的 `CSGCLAW_LARK_CLI_PATH` 或运维配置项，允许显式指定 `lark-cli` 二进制；
2. 增加 OAuth subject guard，私聊/评论 actor 必须匹配授权用户；
3. 把 lark-cli auth/scope 变成更完整的可观测状态；
4. 评估官方 Auth Sidecar，使 App Secret 和用户 Token 留在 CSGClaw 可信控制面；
5. 需要多人共享时，设计按 `operator.open_id` 隔离的 OAuth Token 映射，不能复用首个用户 token。

## 15. 验收标准

当前代码应满足：

- Codex worker 完成飞书连接时，宿主机已有 `lark-cli` 会自动完成 worker 私有绑定；
- 宿主机没有 `lark-cli` 时飞书连接仍然成功，Agent API 返回 `state=unavailable`；
- 未配置 Feishu Bot 的 worker 执行配置会返回 `feishu_bot_not_configured`；
- 已配置 Feishu Bot 的 Codex worker 执行配置会生成独立的 `lark-cli` 和 `lark-cli-source` 目录；
- source config 权限为 `0600`，目录权限为 `0700`；
- `lark-cli config bind` 的环境变量指向当前 worker 的目录；
- runtime 只在 source config 和 bound marker 同时存在时注入 `LARK*` 环境；
- worker 通过 `command_execution` 调用 lark-cli，MCP 代码执行环境的 `not_configured` 结果不能作为绑定
  状态依据；
- Agent Profile 不能覆盖 `LARKSUITE_CLI_CONFIG_DIR`、`LARK_CHANNEL_CONFIG` 等保留变量；
- worker A 和 worker B 的 `LARKSUITE_CLI_CONFIG_DIR` 不同；
- 同一个 AppID 不能同时完成多个 worker 的 lark-cli 初始化；
- 重建 Codex worker 后原有 `<CODEX_HOME>/lark-cli` 和 `<CODEX_HOME>/lark-cli-source` 仍然存在，
  状态保持为已配置；
- 断开 Feishu 后旧的 `<CODEX_HOME>/lark-cli` 和 `<CODEX_HOME>/lark-cli-source` 会被删除；
- 飞书文档评论 prompt 会优先引导已绑定 worker 使用 `lark-cli docs +fetch` 读取 Doc/Docx；
- 已绑定 worker 查找飞书历史附件时，只查询当前隐藏上下文的 `chat_id`，默认显式使用 Bot 身份；
- 唯一匹配的历史附件通过同一条消息的 `message_id + file_key` 单独下载，不使用 CSGClaw 内置渠道的
  附件目录或 `/api/v1/attachments/{id}`。

## 16. 参考

- [lark-cli 官方仓库与安装说明](https://github.com/larksuite/cli)
- [飞书直连渠道与 Agent Engine 当前架构](agent-engine-channel-integration.zh.md)
- [飞书 Channel 配置](feishu.zh.md)
