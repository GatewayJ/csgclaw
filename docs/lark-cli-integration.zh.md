# 将 lark-cli 集成进 CSGClaw Agent 的方案

本文给出一个可落地的集成方案：让 CSGClaw 内的 manager / worker agent 能稳定使用 `lark-cli` 访问飞书能力，并在缺少配置、缺少权限、需要调用 raw API 时给用户和 agent 明确的下一步动作。

结论先行：

- 第一阶段应先交付 `lark-cli` skill，把安装、初始化、授权、常见域能力、raw API 调用规范写清楚，让 agent 立即可用，且不能只覆盖 bot 能力。
- 仅靠 skill 可以完成第一阶段可用闭环：安装检测、配置目录选择、PicoClaw/OpenClaw 凭证探测、初始化、授权和 raw API 使用都先放在 skill 脚本内完成。CSGClaw runtime / UI 集成作为后续体验增强，不是阶段 1 阻塞项。
- 参考 `lark-channel-bridge` 的做法，每个 agent 应使用独立的 `LARKSUITE_CLI_CONFIG_DIR`。第一阶段由 skill 把该目录固定在当前 agent workspace 下的 `.lark-cli`，利用 CSGClaw 已有 workspace 隔离，不需要新增环境变量。
- 当前产品前提是“一个 agent 只服务一个真实用户”。因此 user token 可以按 agent-local 配置目录保存；如果未来支持多人共用同一个 agent，必须升级为 `agent + user` 粒度的 token 隔离。
- 对于缺权限、未初始化、需要用户打开浏览器授权的场景，agent 不应自己猜命令或打开浏览器，而应由 skill 脚本输出结构化下一步动作；CSGClaw UI / Feishu channel / Web UI 可在后续阶段把这些动作产品化。
- raw API 能力必须保留，因为 `lark-cli` 的高层命令不可能覆盖所有飞书 OpenAPI。例如审批实例创建和审批定义列表当前更适合走 `lark-cli api`。

## 背景与目标

CSGClaw 已经具备：

- agent runtime 管理，包含 PicoClaw / OpenClaw sandbox 和 Codex runtime。
- Feishu participant 绑定能力，凭证通过 `csgclaw-cli participant bind` 写入 participant，不再使用旧的 `channels/feishu.toml`。
- skill 搜索与安装能力，`csgclaw skill search/get/install` 可从 OpenCSG / ClawHub registry 安装 skill。
- runtime 环境变量注入能力，例如 PicoClaw runtime 会注入 `CSGCLAW_BASE_URL`、`CSGCLAW_ACCESS_TOKEN` 和已绑定的 Feishu app 凭证；OpenClaw 则把 Feishu app 配置写入 agent-local `openclaw.json`。

`lark-cli` 能提供覆盖飞书多数业务域的能力，包括文档、云空间、多维表格、电子表格、IM、邮件、日历、任务、审批、会议、OKR、应用开发和通用 `api`。把它集成进 CSGClaw 后，agent 可以在用户授权后直接完成更真实的飞书工作流，而不是只停留在聊天转发。

本方案目标：

1. 让 agent 能安装并发现 `lark-cli`。
2. 让 agent 知道如何正确初始化、绑定、授权和检查权限。
3. 让每个 agent 的飞书配置隔离，避免多个 agent 混用同一份 token；当前默认一个 agent 只服务一个真实用户。
4. 当缺权限或未授权时，给出可执行的用户协助流程。
5. 对 `lark-cli api` 提供安全、可追踪、可恢复的使用规范。
6. 完整保留 `lark-cli` 的 bot 与 user 两类能力，不因集成进 CSGClaw 而阉割个人邮箱、日历、云盘、当前用户审批待办等 user-scoped 功能。
7. 保持与 CSGClaw participant / Feishu channel / runtime env 的现有边界一致。

非目标：

1. 不在第一阶段 fork 或重写 `lark-cli`。
2. 不把所有飞书 OpenAPI 都封装成 CSGClaw 原生命令。
3. 不让 skill 直接读写宿主机敏感配置文件或明文 secret。
4. 不绕过飞书 OAuth、管理员审批或 scope 授权流程。
5. 不在镜像内预装 `lark-cli`；安装检测和 bootstrap 由 skill 负责。

## 总体设计

推荐采用“skill 优先、runtime 增强”的分层集成：

```text
用户 / 飞书开放平台 / Feishu OAuth
  -> Agent skill: lark-cli 使用手册和流程约束
  -> skill 脚本: 安装检测、config dir 选择、runtime 凭证探测、授权辅助
  -> lark-cli 高层命令 / lark-cli api
  -> 飞书 OpenAPI

后续增强:
  -> CSGClaw runtime 可选暴露状态 / 授权动作
  -> CSGClaw UI / Feishu channel 授权动作展示
```

三层职责如下：

| 层级 | 职责 | 第一阶段是否必须 |
| --- | --- | --- |
| `lark-cli` skill | 教 agent 何时用、怎么装、怎么初始化、怎么授权、怎么查 schema、怎么用 raw API | 必须 |
| skill 脚本 | bootstrap、选择 agent-local config dir、探测 PicoClaw/OpenClaw 凭证、包装 OAuth device flow | 必须 |
| CSGClaw runtime 集成 | 后续可暴露状态、统一授权动作、减少环境探测 | 非必须，阶段 2 增强 |
| CSGClaw 授权辅助 | 后续可把 OAuth URL、scope 缺失、管理员审批转成用户可操作的授权动作展示 | 非必须，阶段 2 增强 |

这样可以先用 skill 快速获得价值，同时避免把 `lark-cli` 降级成只能用 bot token 的窄工具。第一阶段不要求 UI 交互，但必须有可执行的用户授权闭环：agent 能拿到授权链接和 device code，并能在用户完成浏览器授权后继续原任务。

## Skill 方案

### Skill 名称与触发

建议新增官方 skill：

```text
lark-cli
```

触发描述应覆盖：

- 用户要求操作飞书、Lark、审批、日历、文档、云盘、表格、IM、邮箱、任务等。
- 用户明确提到 `lark-cli`、飞书 OpenAPI、scope、授权、审批单、飞书机器人。
- agent 需要查询 `lark-cli schema` 或调用 `lark-cli api`。

### Skill 内容结构

建议目录：

```text
skills/lark-cli/
  SKILL.md
  references/
    install.md
    auth.md
    api.md
    approval.md
    csgclaw.md
  scripts/
    lark_cli_ready.sh
    lark_cli_bootstrap.sh
    lark_cli_doctor.sh
    lark_cli_bind_app.sh
    lark_cli_auth_start.sh
    lark_cli_auth_complete.sh
```

`SKILL.md` 只放必须规则和决策树，细节放 `references/`，避免 agent 每次加载过多上下文。

这些脚本可以作为第一阶段的主要包装层。脚本只调用稳定的 `lark-cli` 命令，读取当前 runtime 可用的环境变量或本地配置文件，不直接编辑 `lark-cli` 内部配置文件，不解析非结构化日志。

### 必须写入 skill 的规则

#### 1. 优先检查本地状态

agent 在调用飞书前先运行：

```bash
lark-cli config show
lark-cli auth status
```

若 runtime 已经注入 `LARKSUITE_CLI_CONFIG_DIR`，必须尊重该值。未注入时，skill 脚本自行选择 agent-local 持久目录，不应直接切到全局 `~/.lark-cli`。

#### 2. 安装方式

镜像内不会预装 `lark-cli`。使用本 skill 前，agent 必须先运行统一 readiness 脚本：

```bash
scripts/lark_cli_ready.sh
```

脚本行为：

1. 先按规则选择并创建 agent-local `LARKSUITE_CLI_CONFIG_DIR`。
2. 检测 `lark-cli` 二进制或 `npx` 运行路径。
3. 已有运行路径时执行 `lark-cli --version`，再执行 `lark-cli doctor --offline`。
4. 未配置时自动触发绑定逻辑并重试 doctor。

6. 不可用时返回结构化错误，提示用户先安装 Node.js / npm 或提供可用的 `lark-cli`。

确认可用。`ready` 入口在未配置时会尝试绑定，不写入 CSGClaw 镜像，也不假设运行时离线可用。

`lark_cli_ready.sh` 必须幂等：重复运行不应覆盖用户已有授权，也不应重新初始化 app；它只负责确认二进制可用、配置目录可写、必要时绑定并给出下一步动作。

成功时输出最小 JSON，供 agent 判断下一步：

```json
{
  "status": "ready",
  "config_dir": "/home/picoclaw/.picoclaw/workspace/.lark-cli",
  "version": "x.y.z",
  "next": "bind"
}
```

失败时也输出 JSON：

```json
{
  "status": "not-installed",
  "reason": "node_or_npm_missing",
  "hint": "请先安装 Node.js/npm，或手动提供 lark-cli 可执行文件"
}
```

脚本不应强依赖 `jq`。第一阶段的 JSON 输出可以由 shell 生成简单字段；复杂 JSON 解析交给 agent 或后续 wrapper，避免新增 `jq` 缺失导致的失败点。

#### 3. 初始化和绑定方式

独立初始化新飞书应用仍然使用 `config init`：

```bash
lark-cli config init --new
```

已有 App ID / Secret：

```bash
printf '%s' "$APP_SECRET" | lark-cli config init \
  --app-id "$APP_ID" \
  --app-secret-stdin \
  --brand feishu
```

但在 CSGClaw agent 内，不建议让 agent 直接创建第二套平行应用。`lark-cli config init` 在 Agent 上下文中也有保护逻辑，会引导使用 `config bind`，避免覆盖或绕开宿主 agent 已绑定的 app。CSGClaw 内的主路径应是：使用 CSGClaw 管理的 Feishu participant app 凭证，由 skill 脚本调用 `lark-cli config bind` 生成或刷新 `lark-cli` 配置。

推荐脚本：

```bash
scripts/lark_cli_bind_app.sh
```

短期不改 `lark-cli` 的可行做法是让 skill 脚本按 runtime 自适应：

| runtime / 环境 | 优先动作 | 找不到凭证时 |
| --- | --- | --- |
| PicoClaw | 读取 runtime 注入的 `PICOCLAW_CHANNELS_FEISHU_APP_ID` / `PICOCLAW_CHANNELS_FEISHU_APP_SECRET`，生成 `LARK_CHANNEL_CONFIG` 投射配置，再执行 `lark-cli config bind --source lark-channel --identity user-default --force` | 回退到 `lark-cli` 原生 `config init` / `config init --new`，并提示如需接入 CSGClaw Feishu channel，可先运行 `csgclaw-cli participant bind --channel feishu --feishu-kind bot ...` |
| OpenClaw | 直接执行 `lark-cli config bind --source openclaw --identity user-default --force`，由 `lark-cli` 自己读取 `/home/node/.openclaw/openclaw.json` 中的 app 凭证 | 回退到 `lark-cli` 原生初始化流程 |
| Codex / 其他 agent | 默认不假设能从 CSGClaw 取到 Feishu app secret | 使用 `lark-cli` 推荐方式初始化或创建应用 |

当前 CSGClaw 的 Feishu app 映射已经核对：

- `participant bind --channel feishu --feishu-kind bot` 会把 `app_id` / `app_secret` 写入 Feishu agent participant 的 `channel_app_config`。
- CSGClaw API 对外返回 participant 时会把 `channel_app_config.app_secret` 脱敏为 `present`，因此 `lark-cli` 不应尝试通过 CSGClaw HTTP API 反查真实 secret。
- runtime 内部通过 `ParticipantConfigProvider.BotConfigForAgent(agentID)` 读取真实 app secret，再写入或注入到具体 sandbox。
- PicoClaw provisioning 会写 `/home/picoclaw/.picoclaw/config.json` 的 `channels.feishu.app_id` / `app_secret`，同时创建 sandbox 时注入 `PICOCLAW_CHANNELS_FEISHU_APP_ID` / `PICOCLAW_CHANNELS_FEISHU_APP_SECRET`。对 skill 来说，优先读环境变量更合适：它已经代表当前 agent 的 participant app，且避免脚本解析 PicoClaw 私有配置文件。
- OpenClaw provisioning 会写 `/home/node/.openclaw/openclaw.json` 的 `channels.feishu.accounts.<participantID>.appId` / `appSecret`，并设置 `defaultAccount`。当前 `lark-cli config bind --source openclaw` 已支持 OpenClaw 单账号和 `accounts` 多账号结构，因此 OpenClaw 路径应直接使用原生 source。

脚本还应自行选择 agent-local 配置目录。若 `LARKSUITE_CLI_CONFIG_DIR` 未设置，CSGClaw agent 内应固定使用当前 agent workspace 下的 `.lark-cli`：

```bash
if [ -z "${LARKSUITE_CLI_CONFIG_DIR:-}" ]; then
  if [ -d "$HOME/.picoclaw/workspace" ]; then
    export LARKSUITE_CLI_CONFIG_DIR="$HOME/.picoclaw/workspace/.lark-cli"
  elif [ -d "$HOME/.openclaw/workspace" ]; then
    export LARKSUITE_CLI_CONFIG_DIR="$HOME/.openclaw/workspace/.lark-cli"
  elif [ -n "${CODEX_HOME:-}" ]; then
    export LARKSUITE_CLI_CONFIG_DIR="$CODEX_HOME/lark-cli"
  else
    export LARKSUITE_CLI_CONFIG_DIR="$PWD/.lark-cli"
  fi
fi
mkdir -p "$LARKSUITE_CLI_CONFIG_DIR"
chmod 700 "$LARKSUITE_CLI_CONFIG_DIR"
```

这样 skill 可以脱离 CSGClaw 运行；在 CSGClaw 内不要求第一阶段修改 runtime 代码，也不要求新增 `CSGCLAW_AGENT_WORKSPACE` 之类的环境变量。PicoClaw 和 OpenClaw 的 workspace 已经是 per-agent 挂载，`.lark-cli` 放在 workspace 内即可继承隔离。

PicoClaw 下，脚本输入来自 CSGClaw runtime 已有的飞书环境变量，例如：

```bash
LARKSUITE_CLI_CONFIG_DIR=/home/picoclaw/.picoclaw/workspace/.lark-cli
PICOCLAW_CHANNELS_FEISHU_ENABLED=true
PICOCLAW_CHANNELS_FEISHU_APP_ID=cli_xxx
PICOCLAW_CHANNELS_FEISHU_APP_SECRET=...
```

脚本生成的投射配置示例：

```json
{
  "accounts": {
    "app": {
      "id": "cli_xxx",
      "secret": "${PICOCLAW_CHANNELS_FEISHU_APP_SECRET}",
      "tenant": "feishu"
    }
  }
}
```

PicoClaw 路径下，脚本内部只调用稳定 CLI：

```bash
export LARK_CHANNEL_CONFIG="$LARKSUITE_CLI_CONFIG_DIR/csgclaw-lark-channel.json"
lark-cli config bind --source lark-channel --identity user-default --force
```

`--force` 是阶段 1 的默认策略，用于把历史 `bot-only` 配置自动升级为 `user-default`，避免 agent 在飞书会话中无法处理 confirmation required 交互。它只改变本 agent 的 `lark-cli` 身份策略，不会生成 user token，也不会绕过飞书 OAuth、scope 或管理员审批；访问用户资源仍必须走 `auth login`。

如果上述环境变量不存在，脚本不应报死错；它应进入 `lark-cli` 原生初始化路径，或提示用户先通过 `csgclaw-cli participant bind --channel feishu --feishu-kind bot ...` 绑定 Feishu bot app 后重试。

更干净的后续优化是在 `lark-cli` 中新增 `config bind --source picoclaw`：优先读取 `PICOCLAW_CHANNELS_FEISHU_*` 环境变量，必要时再读取 `~/.picoclaw/config.json` 的 `channels.feishu` 配置。这样可以移除 PicoClaw 路径对 `lark-channel` 投射格式的复用。`--source csgclaw` 只有在 CSGClaw 后续决定为所有 runtime 统一生成 agent-local lark-cli source 文件时才需要；当前不是阶段 1 阻塞项。

#### 4. 用户授权方式

需要用户身份时，例如日历、邮箱、云盘个人资源、审批待办：

```bash
lark-cli auth login --domain approval
```

在 agent 会话中更推荐非阻塞流程：

```bash
lark-cli auth login --domain approval --no-wait --json
```

该命令的主路径返回字段应按下面契约处理：

```json
{
  "verification_url": "https://...",
  "device_code": "xxx",
  "expires_in": 600,
  "hint": "Open verification_url in your browser..."
}
```

skill 要求 agent 把 `verification_url` 原样发给用户，并说明用户需要在自己的浏览器里完成授权。agent 不应尝试用自己的 sandbox 浏览器替用户授权，也不应把 no-wait 模式误写成 `verification_uri` / `user_code` 流程。

二维码只作为 best-effort 增强：如果当前 agent/channel 能稳定展示本地图片，可基于 `verification_url` 生成二维码；如果不能稳定展示，直接发送 URL，不引入 Feishu 卡片或图片上传依赖。

授权完成后再轮询：

```bash
lark-cli auth login --device-code "<device_code>"
```

#### 5. 缺权限处理

当 `lark-cli` 返回缺 scope、未配置、未登录、管理员审批未完成等错误时，skill 必须：

- 保留原始错误中的 `type`、`subtype`、`missing_scopes`、`hint`、`log_id`。
- 不盲目重试。
- 先判断是 app/bot 权限不足，还是 user token 缺 scope。
- 给出下一步命令，例如 `lark-cli auth login --scope ...` 或要求管理员在飞书开放平台开通权限。

示例恢复流程：

```text
1. 发现 missing_scopes=["approval:approval.list:readonly"]。
2. 提醒用户该权限需要应用开通，可能需要管理员审核。
3. 若是 user 权限，发起 `lark-cli auth login --scope approval:approval.list:readonly --no-wait --json`。
4. 把返回的 verification_url 原样交给用户。
5. 用户完成授权后，使用 device_code 完成登录。
6. 重新执行原命令。
```

#### 6. `lark-cli api` 使用规范

当高层命令不存在或 schema 不覆盖时，允许使用：

```bash
lark-cli api <METHOD> <PATH> --params '<json>' --data '<json>' --as bot|user
```

调用前必须确认：

- 官方 OpenAPI 路径。
- token 类型，`--as bot` 还是 `--as user`。
- 请求参数和请求体 JSON。
- 是否分页，是否需要 `--page-all`。
- 是否写操作，是否需要用户确认。

示例：查询当前用户可发起的审批定义列表：

```bash
lark-cli api GET /open-apis/approval/v4/approvals \
  --as user \
  --params '{"page_size":100,"locale":"zh-CN"}' \
  --page-all
```

示例：按审批定义查询审批实例：

```bash
lark-cli api POST /open-apis/approval/v4/instances/query \
  --as bot \
  --params '{"page_size":20,"user_id_type":"open_id"}' \
  --data '{"approval_code":"<approval_code>","instance_status":"ALL","locale":"zh-CN"}'
```

skill 需要强调：raw API 是能力补充，不是绕过权限。所有写操作仍需用户确认和权限检查。

## CSGClaw Runtime 集成方案

只靠 skill 可以完成第一阶段闭环：安装、agent-local 配置目录选择、PicoClaw/OpenClaw 凭证探测、初始化和授权动作都可以先由 skill 脚本包装完成。CSGClaw runtime 层后续可选补充状态展示、统一变量或授权动作展示，以提升体验，而不是第一阶段的阻塞项。

### 每个 agent 独立配置目录

参考 `lark-channel-bridge` 的 profile-local 设计，每个 agent 都应使用独立的 `lark-cli` 配置目录。CSGClaw 代码已经把 PicoClaw / OpenClaw workspace 放在各自 agent 的 runtime root 内，因此第一阶段直接把 `LARKSUITE_CLI_CONFIG_DIR` 放在当前 agent workspace 下：

```text
PicoClaw 容器内: /home/picoclaw/.picoclaw/workspace/.lark-cli
OpenClaw 容器内: /home/node/.openclaw/workspace/.lark-cli

宿主机对应:
~/.csgclaw/agents/<agent-name>/.picoclaw/workspace/.lark-cli
~/.csgclaw/agents/<agent-name>/.openclaw/workspace/.lark-cli
```

如果 runtime 已经注入 `LARKSUITE_CLI_CONFIG_DIR`，skill 直接尊重该值。未注入时，skill 依据当前容器内已存在的 `$HOME/.picoclaw/workspace` 或 `$HOME/.openclaw/workspace` 选择目录。Codex 不属于 PicoClaw/OpenClaw gateway runtime，阶段 1 仍按 `$CODEX_HOME/lark-cli` 或 `$PWD/.lark-cli` 兜底。

PicoClaw 已经会按绑定状态注入：

```bash
PICOCLAW_CHANNELS_FEISHU_ENABLED=true
PICOCLAW_CHANNELS_FEISHU_APP_ID=cli_xxx
PICOCLAW_CHANNELS_FEISHU_APP_SECRET=...
```

`PICOCLAW_CHANNELS_FEISHU_APP_ID` / `PICOCLAW_CHANNELS_FEISHU_APP_SECRET` 来自 Feishu bot participant 的 `channel_app_config`，只用于 `lark_cli_bind_app.sh` 生成 agent-local 投射配置并调用 `lark-cli config bind`，不应打印到日志或响应。投射文件中的 secret 应写成 `"${PICOCLAW_CHANNELS_FEISHU_APP_SECRET}"` 环境变量引用，不应展开成明文。这样：

- manager 和 worker 不共享用户 token。
- 不同 Feishu bot / participant 不互相污染。
- 删除或重建 agent 时可以清晰处理本 agent 的 lark-cli 状态。
- agent 日志中不需要暴露宿主真实路径。

OpenClaw 不使用上述 PicoClaw 环境变量。CSGClaw 已把 Feishu app 写入 agent-local `openclaw.json` 的 `channels.feishu.accounts.<participantID>`，而 `lark-cli` 的 OpenClaw binder 已支持该结构，所以 `lark_cli_bind_app.sh` 应直接调用 `lark-cli config bind --source openclaw --identity user-default --force`，不再生成 `LARK_CHANNEL_CONFIG`。

Codex runtime 当前不默认提供 Feishu app secret。第一阶段不要假设可以从 CSGClaw API 取回 secret，因为 participant API 会脱敏 `app_secret`。Codex 下应走 `lark-cli` 原生初始化或创建应用流程。

### 身份策略

建议在 agent profile 或 participant binding 中增加：

```toml
[lark_cli]
enabled = true
identity_preset = "user-default" # user-default | bot-only
auto_bind_feishu_app = true
user_auth_required = "on-demand" # on-demand | required | disabled
```

策略含义：

| 策略 | 行为 | 适用场景 |
| --- | --- | --- |
| `user-default` | 保留 app / bot 身份，同时允许用户授权后的 user 身份；执行命令时按 `--as bot` / `--as user` 选择 | 推荐默认。完整飞书能力，包括日历、邮箱、云盘、当前用户审批待办 |
| `bot-only` | 只使用 app / bot 身份，不访问个人资源 | 仅适合明确的服务端自动化、机器人发送、租户级查询、CI 或无真人授权环境 |

本方案不应把 `bot-only` 当作默认产品能力。`bot-only` 只能作为启动底座或安全降级模式；如果用户希望 agent 操作个人邮箱、日历、云盘、审批待办等能力，CSGClaw 必须支持 `user-default`。

`user-default` 不表示 agent 可以无条件冒充用户。它表示当前 agent 的 `lark-cli` 配置目录中允许保存 user token；真正访问用户资源前仍需要用户通过飞书 OAuth 明确授权。授权完成前，agent 调用 `--as user` 应返回可操作的授权请求，而不是静默失败。

### 使用 Feishu participant 凭证绑定

当 agent 绑定了 Feishu bot participant，CSGClaw 已经知道：

- `channel_app_config.app_id`
- `channel_app_config.app_secret`
- `agent_id`

CSGClaw 不应直接写 `lark-cli` 内部配置文件。第一阶段由 skill 脚本根据当前 runtime 自行探测可复用的 app 凭证，再由 `lark-cli` 自己生成正式配置，使 agent 不需要重复输入 App Secret。探测不到时不阻塞，回退到 `lark-cli` 原生初始化流程。

建议新增内部流程：

```text
participant bind 写入 Feishu app 凭证
  -> skill 脚本检查 lark-cli 是否安装
  -> skill 脚本选择 agent-local LARKSUITE_CLI_CONFIG_DIR
  -> PicoClaw: 读取 PICOCLAW_CHANNELS_FEISHU_APP_ID / PICOCLAW_CHANNELS_FEISHU_APP_SECRET
       -> 生成 LARK_CHANNEL_CONFIG
       -> lark-cli config bind --source lark-channel --identity user-default --force
  -> OpenClaw: lark-cli config bind --source openclaw --identity user-default --force
  -> Codex/其他/未绑定: lark-cli config init 或 lark-cli config init --new
  -> agent 可直接 `lark-cli api --as bot ...`
  -> 当需要用户资源时，通过授权辅助补齐 user token
```

第一阶段不能只停在 bot 配置。可以不预先生成 user token，因为 user token 必须由真人授权产生；但必须同时交付 user 授权入口，让 agent 在需要 `--as user` 时能引导用户完成授权。

### 状态展示

第一阶段至少由 skill 脚本在 stdout/stderr 中区分关键状态，便于 agent 决策。后续增强时，CSGClaw UI、CLI 和 Feishu `/status` 类命令可统一展示：

```text
lark-cli: not-installed | not-configured | bot-ready | user-ready | missing-scope | admin-approval-required | oauth-pending
app: cli_xxx
identity: user-default
user_auth: not-started | oauth-pending | ready | missing-scope | admin-approval-required
config_dir: agent-local
```

不要展示 secret、access token、refresh token。

## 授权辅助方案

`lark-cli` 初始化和 OAuth 登录通常需要用户打开浏览器。第一阶段可以由 skill 脚本包装授权命令，要求脚本输出结构化 JSON，再由 agent 把授权链接转交给用户；后续如有需要，再由 CSGClaw UI / Feishu channel 做成统一授权动作展示。

### `lark-channel-bridge` 的做法

`lark-channel-bridge` 可以作为参考，但不能直接照搬为 CSGClaw 的用户 OAuth 方案。它实际拆成了两层：

1. 首次运行时，bridge 用终端二维码 wizard 让用户绑定或创建 PersonalAgent app，并把 app 配置写入 `~/.lark-channel/config.json`。这一步解决的是 bot app 凭证，不是 `lark-cli` 用户 OAuth。
2. bridge 为每个 profile 创建独立的 `~/.lark-channel/profiles/<profile>/lark-cli` 目录，并把它注入给 agent 子进程作为 `LARKSUITE_CLI_CONFIG_DIR`。
3. bridge 生成 profile-local 的 `lark-cli-source/config.json` 投射文件，内容包含 app id、tenant，以及 secret 引用；agent 子进程同时拿到 `LARK_CHANNEL_CONFIG`，再由 `lark-cli config bind --source lark-channel ...` 读取。
4. bridge 默认偏保守，初始化时多使用 `bot-only`；当已有同 app 用户授权、用户在 `/config` 切换，或 agent 完成用户授权后，再通过 `strict-mode off` / `default-as auto` 收敛到 user-default。
5. 对 `lark-cli auth login`，bridge 没有单独做 Feishu 卡片授权系统；它在系统提示里要求 agent 使用两阶段流：先 `auth login --no-wait --json` 拿 `verification_url` 和 `device_code`，把 URL 原样发给用户，再前台执行 `auth login --device-code <code>` 等待用户完成。bridge 的 idle watchdog 会在工具调用运行时暂停，避免长时间授权被误杀。
6. bridge 对错误恢复没有完整状态机：安装和 bind 在 preflight 中自动处理；`/status` 只展示 `app`、`user-ready`、`user-missing`、`check-failed` 这类粗状态；缺 scope、管理员审批、具体 API 权限错误主要依赖 `lark-cli` 结构化错误和 agent 后续判断。

CSGClaw 采用其中三点：agent-local 配置目录、app 凭证投射、`verification_url + device_code` 两阶段授权。不同点是 CSGClaw 的前提是“一个 agent 只服务一个真实用户”，所以阶段 1 可以直接 `user-default --force`，并把用户 OAuth 包装成 `auth_start` / `auth_complete` 两个脚本，避免要求 agent 在一次前台阻塞命令里等完整授权。

### 错误恢复规则

参考 `lark-channel-bridge` 的边界，CSGClaw skill 应把基础配置问题自动化，把权限和真人授权问题显式交给用户处理。agent 遇到以下状态时按固定规则恢复，不应盲目重试：

| 状态 | 典型触发 | agent 处理 | 用户可见动作 |
| --- | --- | --- | --- |
| `not-installed` | `command -v lark-cli` 失败，或 bootstrap 检查失败 | 运行 `lark_cli_bootstrap.sh`；可安装时安装并重新执行 `doctor --offline`；不可安装时停止飞书 API 调用 | 提示安装 Node.js/npm 或手动提供 `lark-cli` |
| `not-configured` | `lark-cli config show` 返回未配置，或提示需要 `config bind` | 运行 `lark_cli_bind_app.sh`；PicoClaw 读 `PICOCLAW_CHANNELS_FEISHU_*`；OpenClaw 用 `--source openclaw`；成功后重试原命令 | 若找不到 app 凭证，提示先绑定 Feishu bot app 或走 `lark-cli config init` |
| `oauth-pending` | `auth_start` 已返回 `verification_url` / `device_code`，用户尚未完成授权 | 暂停原任务；保存未过期的 `device_code`；等待用户确认后调用 `lark_cli_auth_complete.sh`；超时后重新 start | 展示授权用途和 `verification_url`，要求用户在浏览器完成授权 |
| `missing-scope` | 错误中包含 `missing_scopes`，或 API 返回权限不足且给出 scope | 保留 `type`、`subtype`、`missing_scopes`、`hint`、`log_id`；判断是 user scope 还是 app/admin scope；不能判断时不要自动重试 | user scope 走 `auth_start --scope ...`；app/admin scope 提示管理员在飞书开放平台开通 |
| `admin-approval-required` | scope 已请求但飞书提示等待管理员审核，或 API hint 指向管理员审批 | 停止重试原 API；把状态标记为等待管理员审批；保留 `log_id` 和 scope 列表 | 明确告知用户需要管理员审批，审批完成后再重试 |

恢复时必须遵守：

- stdout 只输出结构化 JSON；解释、提示和诊断走 stderr，避免破坏管道。
- 每次恢复最多执行一个明确动作，例如 bootstrap、bind、auth_start、auth_complete 或 retry original command。
- 缺 scope、管理员审批、OAuth 等状态不得循环重试；只有用户完成授权或管理员确认权限已开通后才能重试原命令。
- 错误中的 `missing_scopes`、`log_id`、`hint` 必须保留给用户或后续 UI，不能只输出自然语言总结。
- 不展示 App Secret、access token、refresh token、device_code。`device_code` 只在脚本内部或当前 agent 轮次中用于 complete，不应发到群聊或长期保存。

### 未初始化

当 agent 运行 `lark-cli config show` 返回 `not_configured`：

1. agent 先调用 `scripts/lark_cli_ready.sh`（其中包含绑定步骤）。
2. 如果是 PicoClaw 且存在 `PICOCLAW_CHANNELS_FEISHU_APP_ID` / `PICOCLAW_CHANNELS_FEISHU_APP_SECRET`，脚本生成 `LARK_CHANNEL_CONFIG` 并执行 `lark-cli config bind --source lark-channel --identity user-default --force`。
3. 如果是 OpenClaw，脚本执行 `lark-cli config bind --source openclaw --identity user-default --force`，由 `lark-cli` 自己读取 OpenClaw 配置。
4. 如果找不到可复用凭证，脚本返回明确提示：使用 `lark-cli config init` / `lark-cli config init --new`，或先执行 `csgclaw-cli participant bind --channel feishu --feishu-kind bot ...` 后重试。
5. 如果当前任务需要用户资源，则继续进入用户授权流程，不能因为 bot 配置存在就把任务判定为已满足。

### 需要用户授权

当 agent 需要 user token：

1. agent 调用 `scripts/lark_cli_auth_start.sh --domain <domain>`。
2. 脚本执行 `lark-cli auth login --domain <domain> --no-wait --json`，并输出 `verification_url`、`device_code`、`expires_in`、`hint`。
3. agent 把 `verification_url` 和授权用途发给用户；URL 必须原样展示，不做 Markdown 链接改写或 URL 编码。
4. 用户完成浏览器授权后告知 agent。
5. agent 调用 `scripts/lark_cli_auth_complete.sh --device-code <device_code>`。
6. 脚本在该 agent 的 config dir 下执行 `lark-cli auth login --device-code ...`。
7. 成功后恢复原任务或提示 agent 重试。

如果当前 channel 支持稳定展示图片，`lark_cli_auth_start.sh` 可选生成二维码文件并在 JSON 中返回路径；不支持时只返回 `verification_url`，不影响主流程。

### 缺 scope

当返回缺 scope：

```json
{
  "type": "permission",
  "missing_scopes": ["approval:approval.list:readonly"]
}
```

第一阶段 skill 脚本应输出两种处理建议；后续 CSGClaw 可把它们转成 UI / Feishu channel 动作：

- user scope：提示 agent 运行 `auth login --scope ... --no-wait --json`，并把授权链接交给用户。
- app scope：提示管理员到飞书开放平台开通权限，并显示所需 scope 和对应业务用途。

如果权限需要管理员审批，系统应明确显示“等待管理员审批”，而不是反复重试。

## raw API 辅助

`lark-cli api` 很重要，但对 agent 来说容易出错。第一阶段先由 skill 规则约束；后续如果使用频率高，再提供 CSGClaw 轻量辅助，而不是重写整个 `lark-cli`。

### Skill 内规则

skill 要求 agent：

1. 先尝试 `lark-cli schema <service.resource.method>`。
2. 如果 schema 不存在，再查官方 API 文档或已知 reference。
3. 明确 `--as bot` / `--as user`。
4. 对写操作向用户确认。
5. 对分页使用 `--page-all` 或显式处理 `page_token`。

### CSGClaw 后续增强

可以新增一个代理命令：

```bash
csgclaw-cli lark api --agent <agent-id> \
  --as bot \
  GET /open-apis/approval/v4/approvals \
  --params '{"page_size":100}'
```

它不替代 `lark-cli`，只负责：

- 自动选择 agent 的 `LARKSUITE_CLI_CONFIG_DIR`。
- 统一脱敏。
- 捕获结构化错误并转成 CSGClaw action。
- 可选记录审计日志。

这样 agent 仍能使用原生 `lark-cli`，但产品层可控。

## 安装与注册流程

### 用户视角

推荐用户路径：

```bash
# 1. 安装或更新 CSGClaw
csgclaw onboard

# 2. 绑定 Feishu bot app 到 manager 或 worker
printf '%s' "$APP_SECRET" | csgclaw-cli participant bind \
  --channel feishu \
  --feishu-kind bot \
  --agent u-manager \
  --app-id cli_xxx \
  --app-secret-stdin \
  --restart

# 3. 安装 lark-cli skill
csgclaw skill install lark-cli

# 4. agent 首次使用 skill 时自动 bootstrap + bind；用户可检查 agent 状态
csgclaw agent list
csgclaw agent logs u-manager -n 50
```

如果 CSGClaw 内置默认技能包，`lark-cli` skill 可以随 manager 模板预装，不要求用户手动安装。

### 运行环境视角

manager / worker runtime 镜像不预装 `lark-cli`。skill 负责在首次使用时检测和安装。运行环境需要满足：

- Node.js / npm / npx 可用，或用户已经手动提供 `lark-cli` 可执行文件。
- 网络允许访问 npm 安装源，或用户配置了可用的 npm mirror。

镜像不应内置任何真实 App ID / Secret / Token。若运行环境没有 Node.js / npm / npx，`lark_cli_bootstrap.sh` 应返回明确错误，不继续执行飞书 API 调用。

### 注册表视角

`lark-cli` skill 应发布到 OpenCSG skill registry，并标记为官方、安全、非可疑：

```bash
csgclaw skill search lark-cli
csgclaw skill install lark-cli
```

后续可以把它加入 manager 模板默认 skills，使新建 manager 天然知道如何使用飞书能力。

## 分阶段实施

### 阶段 0：文档与 skill

交付：

- 新增 `lark-cli` skill。
- skill 覆盖安装检测、按需安装、绑定、授权、权限恢复、schema、raw API。
- skill 提供 `lark_cli_bootstrap.sh`、`lark_cli_bind_app.sh`、`lark_cli_auth_start.sh`、`lark_cli_auth_complete.sh` 等脚本包装。
- 补充 CSGClaw 文档，说明与 Feishu participant bind 的关系。

验收：

- agent 能根据 skill 正确解释 `lark-cli config bind`、`auth login`、`api`。
- 未安装 `lark-cli` 时，agent 能先执行 bootstrap 脚本安装；无法安装时返回 `not-installed`、`reason`、`hint` 等结构化错误。
- agent 遇到 `not_configured`、缺 scope 时不会盲目重试。

### 阶段 1：skill 自适应与 user-default 最小闭环

交付：

- skill 脚本将 agent-local `LARKSUITE_CLI_CONFIG_DIR` 固定在当前 agent workspace 的 `.lark-cli` 下，优先使用 `$HOME/.picoclaw/workspace/.lark-cli` 或 `$HOME/.openclaw/workspace/.lark-cli`。
- PicoClaw 下优先读取 `PICOCLAW_CHANNELS_FEISHU_APP_ID` / `PICOCLAW_CHANNELS_FEISHU_APP_SECRET`。
- PicoClaw 下生成 agent-local `LARK_CHANNEL_CONFIG` 投射配置，并调用稳定的 `lark-cli config bind --source lark-channel --identity user-default --force` 生成 agent-local app 配置，使 `--as bot` 可用。
- OpenClaw 下调用 `lark-cli config bind --source openclaw --identity user-default --force`，由 `lark-cli` 自己读取 OpenClaw app 凭证。
- Codex / 其他 runtime / 未绑定 Feishu 凭证时，回退到 `lark-cli config init` / `lark-cli config init --new`。
- 默认身份策略支持 `user-default`，即同一配置目录允许后续保存 user token。
- skill 脚本提供 `auth login --no-wait --json` 到 `auth login --device-code` 的最小授权闭环，主字段为 `verification_url`、`device_code`、`expires_in`、`hint`。

验收：

- worker A 和 worker B 使用不同的 `LARKSUITE_CLI_CONFIG_DIR`，`lark-cli auth status` 不互相影响。
- PicoClaw / OpenClaw 已绑定 Feishu 凭证时，agent 能直接执行 `lark-cli api --as bot ...`。
- 找不到可复用凭证时，agent 能按 `lark-cli` 原生提示完成初始化，而不是卡死在 CSGClaw 专用流程。
- agent 请求访问用户审批待办、邮箱、日历或云盘时，用户能完成授权并让 `lark-cli api --as user ...` 成功。
- 日志和 API 响应不泄露 App Secret。

### 阶段 2：状态与授权体验增强

交付：

- CLI / UI 展示 `lark-cli` 状态。
- 可选将阶段 1 的授权闭环产品化为 Web UI / Feishu channel 授权动作展示。
- 展示 `verification_url`、所需 scopes 和授权用途；如需内部续接，保存未过期的 `device_code`。
- 用户完成授权后，CSGClaw 自动执行 `device_code` 完成登录，并恢复原任务。
- 缺 scope 时生成用户授权或管理员开通权限的 action。

验收：

- agent 请求访问用户审批待办时，用户能在 UI 中完成授权。
- 授权后 agent 自动重试或收到明确的可重试提示。

### 阶段 3：raw API 包装增强

交付：

- 新增 `csgclaw-cli lark api` 或内部 tool wrapper。
- 统一设置 config dir、脱敏、错误分类。
- 可选增加审计和高风险写操作确认门。

验收：

- agent 调用 raw API 时错误消息更清晰。
- wrapper 能选择 agent-local `LARKSUITE_CLI_CONFIG_DIR`，并保持 token/secret 不外泄。

## 可行性 Review

结论：在“一个 agent 只服务一个真实用户”的前提下，本方案可行，可以进入阶段 0 / 阶段 1 开发。阶段 1 主要放在 skill 脚本内完成，不要求先改 CSGClaw runtime：PicoClaw 使用 `LARK_CHANNEL_CONFIG` 兼容投射，OpenClaw 使用现有 `config bind --source openclaw`，Codex 默认走 `lark-cli` 原生初始化。中期在 `lark-cli` 增加 `config bind --source picoclaw` 会让 PicoClaw 路径边界更干净，但不是阻塞项。

已核对的现有能力：

- `lark-cli` npm 安装入口存在，支持 `npx @larksuite/cli@latest install`。
- `lark-cli doctor --offline` 存在，可用于 bootstrap 的本地健康检查。
- `lark-cli config bind --source lark-channel --identity user-default --force` 存在，且 `lark-channel` binder 支持 `LARK_CHANNEL_CONFIG` 指定配置文件，也支持 `"${ENV_NAME}"` 形式的 secret 引用。
- `lark-cli config bind --source openclaw --identity user-default --force` 存在，可直接复用 OpenClaw agent-local 配置中的 Feishu app 凭证，并支持 `channels.feishu.accounts` 多账号结构。
- `lark-cli config init --app-secret-stdin` 存在，但它不是 CSGClaw agent 内的主路径；Agent 上下文应使用 `config bind`，避免创建第二套 app。
- `lark-cli auth login --no-wait --json` 与 `lark-cli auth login --device-code <code>` 存在，可支撑非阻塞 OAuth 闭环。
- CSGClaw 现有 runtime 已能向 PicoClaw 注入 `PICOCLAW_CHANNELS_FEISHU_APP_ID` / `PICOCLAW_CHANNELS_FEISHU_APP_SECRET`，并向 OpenClaw `openclaw.json` 写入 `channels.feishu.accounts.<participantID>`。

阶段 1 必须开发或验证的点：

- `lark_cli_bind_app.sh` 能把 `LARKSUITE_CLI_CONFIG_DIR` 放在当前 agent workspace 的 `.lark-cli` 下，并验证目录可写、权限为 `0700`。
- `lark_cli_bind_app.sh` 能在 PicoClaw 下根据 `PICOCLAW_CHANNELS_FEISHU_*` 生成 `LARK_CHANNEL_CONFIG` 投射配置。
- `lark_cli_bind_app.sh` 能在 OpenClaw 下调用 `lark-cli config bind --source openclaw --identity user-default --force`。
- Codex 下找不到 CSGClaw 飞书 app secret 时，脚本必须回退到 `lark-cli` 原生初始化流程。
- Linux/container 下 `lark-cli` keychain 使用本地加密文件，需验证其文件写入目录在 agent workspace 的 `.lark-cli` 下可用；macOS sandbox 场景按 `lark-cli` 的 keychain 提示处理。
- `user-default --force` 是默认 bind 策略，但 user token 仍必须由真人 OAuth 授权产生。
- 状态展示可后续增强，至少在脚本输出中区分 `not-installed`、`not-configured`、`bot-ready`、`user-ready`、`oauth-pending`、`missing-scope`、`admin-approval-required`。

## Skill 不足时的替代方案

如果实践证明 skill 无法稳定约束 agent，可以升级为 CSGClaw 原生 tool。

### 方案 A：`csgclaw-cli lark` 命令组

新增：

```bash
csgclaw-cli lark status --agent <agent-id>
csgclaw-cli lark bind --agent <agent-id> --identity user-default
csgclaw-cli lark auth-start --agent <agent-id> --domain approval
csgclaw-cli lark auth-complete --agent <agent-id> --device-code xxx
csgclaw-cli lark api --agent <agent-id> --as bot GET /open-apis/...
```

优点：

- 产品体验稳定。
- 不依赖 agent 是否严格遵守 skill。
- 方便做统一动作展示和审计。

缺点：

- 实现成本高于纯 skill。
- 需要维护一层 wrapper 与 `lark-cli` 输出兼容。

### 方案 B：内置 MCP / tool bridge

为 CSGClaw agent 暴露一个内部工具：

```text
lark.status
lark.schema
lark.api
lark.auth.start
lark.auth.complete
```

工具内部调用 `lark-cli`，并把错误统一成 agent 友好的结构。

优点：

- agent 调用更稳定，不需要拼 shell JSON。
- 可强制高风险操作确认。

缺点：

- 与 runtime/tooling 绑定更深。
- 初期开发和测试面更大。

### 方案 C：纯 skill 仅作临时过渡

只把 `lark-cli` skill 用作“指导文档”，真实飞书能力仍通过人工或已有 Feishu channel 完成。这只能作为过渡方案，不满足“不阉割 lark-cli 能力”的目标。

优点：

- 成本最低。

缺点：

- 无法解决授权和配置隔离。
- 无法稳定使用邮箱、日历、云盘、当前用户审批待办等 user-scoped 能力。
- agent 成功率会受 shell 环境、权限、网络和用户配合影响。

推荐路径是：阶段 0 使用 skill 快速试运行；阶段 1 只依赖 skill 脚本打通 `user-default` 最小授权闭环；后续如果授权交互、状态展示或 raw API 使用量变大，再做 runtime/UI 增强、方案 A 或方案 B。

## 风险与对策

| 风险 | 对策 |
| --- | --- |
| 多 agent 混用 token | 每个 agent 独立 `LARKSUITE_CLI_CONFIG_DIR` |
| 未来多人共用同一个 agent | 当前明确不支持；若后续支持，升级为 `agent + user` 粒度配置目录 |
| secret 泄露 | secret 只进入 participant store / agent-local config，API 和日志统一脱敏 |
| `bot-only` 导致能力被阉割 | 默认支持 `user-default`，bot 配置只作为 app 身份底座；用户资源通过 OAuth 补齐 |
| OAuth 卡住 agent | 使用 `--no-wait --json`，第一阶段由 agent 转交授权链接；后续再接入 UI / Feishu channel |
| 缺 scope 后反复失败 | 捕获 `missing_scopes`，生成授权或管理员审批动作 |
| raw API 参数难写 | skill 要求先查 schema/文档；后续提供 `csgclaw-cli lark api` wrapper |
| 高风险写操作误执行 | skill 要求用户确认；wrapper 阶段可再加强确认门 |
| 运行环境无法安装 `lark-cli` | bootstrap 脚本先检查 `lark-cli` / Node.js / npx；不可用时返回结构化安装提示 |
| 飞书 API 变更 | skill 中保留“先查 schema / 官方文档”的规则，wrapper 不硬编码过多业务域 |

## 推荐落地顺序

1. 编写并发布 `lark-cli` skill。
2. 在 skill 中实现 `lark_cli_bootstrap.sh`，首次使用时检测并按需安装 `lark-cli`。
3. 在 skill 中实现 `lark_cli_bind_app.sh`，将 agent-local `LARKSUITE_CLI_CONFIG_DIR` 放在当前 agent workspace 的 `.lark-cli` 下。
4. 在 `lark_cli_bind_app.sh` 中实现 PicoClaw 路径：读取 `PICOCLAW_CHANNELS_FEISHU_APP_ID` / `PICOCLAW_CHANNELS_FEISHU_APP_SECRET`，生成 `LARK_CHANNEL_CONFIG`，调用 `lark-cli config bind --source lark-channel --identity user-default --force`。
5. 在 `lark_cli_bind_app.sh` 中实现 OpenClaw 路径：调用 `lark-cli config bind --source openclaw --identity user-default --force`。
6. 在 `lark_cli_bind_app.sh` 中实现 Codex / 其他 / 未绑定回退：按 `lark-cli` 推荐方式提示或执行 `config init` / `config init --new`。
7. 默认启用 `user-default` 能力路径，并通过 skill 脚本实现 `--no-wait` / `device-code` 授权闭环。
8. 脚本输出中区分 `not-installed`、`not-configured`、`bot-ready`、`user-ready`、`oauth-pending`、`missing-scope`、`admin-approval-required`。
9. 后续再考虑 Web UI / Feishu channel 授权动作展示和统一状态展示。
10. 后续在 `lark-cli` 增加 `config bind --source picoclaw`，替代 PicoClaw 路径的 `lark-channel` 兼容投射；仅当 CSGClaw 后续统一生成 lark-cli source 文件时，再考虑 `--source csgclaw`。
11. 根据使用频率决定是否实现 `csgclaw-cli lark api` 或内部 MCP/tool bridge。

这个顺序能让第一阶段保留完整 `lark-cli` 能力，同时把 bot 身份配置、用户 OAuth、raw API 和权限恢复拆成可验证的闭环。
