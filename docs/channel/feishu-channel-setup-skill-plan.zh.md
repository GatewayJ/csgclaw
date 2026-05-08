# CSGClaw 飞书 Channel 自动接入 Skill 方案

## 1. 结论先行

当前代码已经具备“飞书消息进入 worker”的基础能力，不需要新增一个飞书开放平台 HTTP webhook callback 接口。

原因是：worker 由 PicoClaw 实现，而 PicoClaw 的 Feishu channel 已经使用飞书/Lark SDK 的 WebSocket mode 接收消息：

- PicoClaw 代码：`/home/jhw/opcsg/picoclaw/pkg/channels/feishu/feishu_64.go`
- 启动时构造 `larkws.NewClient(...)`
- 注册 `OnP2MessageReceiveV1(c.handleMessageReceive)`
- 收到飞书消息后进入 `BaseChannel.HandleMessage(...)`

也就是说，飞书平台到 worker 的入站链路由 PicoClaw Feishu WebSocket/SDK 负责，不需要让 CSGClaw 再新增一个公开的飞书 webhook URL。

CSGClaw 当前的 `/api/v1/channels/feishu/bots/{bot}/events` 不是飞书开放平台 webhook，而是 CSGClaw 给 PicoClaw worker 用的内部 SSE 事件流：当 manager 在 CSGClaw/前端里通过飞书 channel @ 某个 worker bot 时，worker 的 PicoClaw Feishu channel 可以订阅这个 SSE，把 CSGClaw 内部消息转换成 PicoClaw 的 Feishu 入站消息格式。

因此，本方案应从“新增飞书 webhook”修正为：

1. 复用 PicoClaw 已有 Feishu WebSocket/SDK 入站能力。
2. 复用 CSGClaw 已有 Feishu SSE 事件流能力。
3. 新增或补齐的是“自动创建/选择飞书 bot、自动安全写入 channel 配置、重启/刷新 worker、doctor 验证”的编排能力。

## 2. 目标 user story

用户部署 CSGClaw 后，在 Web 前端和 manager 聊天：

> 对接飞书机器人

期望流程：

1. manager 调用 skill，告诉用户去创建或选择一个飞书机器人。
2. 用户完成飞书开放平台授权/选择后，skill 获取该 bot 的 `app_id` 和 `app_secret`。
3. skill 不把 secret 明文回显给用户，只把凭证安全写入 CSGClaw channel 配置。
4. skill 调用 `csgclaw-cli` 或等价 API 创建/绑定 manager bot，例如：
   ```bash
   csgclaw-cli bot create --id u-manager --name manager --role manager --description "your manager agent" --channel feishu
   ```
5. CSGClaw 启动/刷新后，manager 可以在飞书里收发消息。
6. 用户后续可以在飞书里对 manager 说：
   > 帮我创建一个 dev 飞书机器人
7. manager 再走同样流程，为 worker `u-dev` 创建/选择飞书 bot，并把 `app_id/app_secret` 配置到该 worker。
8. worker 使用 PicoClaw Feishu WebSocket/SDK 接收飞书消息；如果它还需要接收 CSGClaw 内部转发的飞书消息，则配置 PicoClaw 的 CSGClaw SSE 参数。

## 3. 当前代码已具备能力

### 3.1 CSGClaw 可以配置多个飞书 bot app

当前 CSGClaw 配置结构在主配置中支持：

```toml
[channels.feishu]
admin_open_id = "ou_xxx"

[channels.feishu.u-manager]
app_id = "cli_xxx"
app_secret = "[REDACTED]"

[channels.feishu.u-dev]
app_id = "cli_xxx"
app_secret = "[REDACTED]"
```

对应代码：

- `/home/jhw/opcsg/csgclaw/internal/config/config.go`
  - `ChannelsConfig.Feishu map[string]FeishuConfig`
  - `FeishuConfig.AppID`
  - `FeishuConfig.AppSecret`

这说明 CSGClaw 已经可以按 CSGClaw bot id 保存多个飞书 app 凭证，例如 `u-manager`、`u-dev`。

### 3.2 CSGClaw 有飞书 channel service 和发送能力

对应代码：

- `/home/jhw/opcsg/csgclaw/internal/channel/feishu.go`
- `/home/jhw/opcsg/csgclaw/internal/api/feishu.go`

当前 FeishuService 已支持：

- 解析 bot 对应的飞书 app 配置。
- 通过飞书 API 获取 bot open_id。
- 创建/列出飞书用户和房间。
- 发送消息。
- 当消息包含 mention 时发布 `FeishuMessageEvent`。

### 3.3 CSGClaw 已有给 PicoClaw worker 订阅的 Feishu SSE 事件流

路由：

```text
GET /api/v1/channels/feishu/bots/{bot}/events
Authorization: Bearer <server access token>
Accept: text/event-stream
```

对应代码：

- `/home/jhw/opcsg/csgclaw/internal/api/router.go`
- `/home/jhw/opcsg/csgclaw/internal/api/feishu.go`

这个 endpoint 的行为：

1. 校验 CSGClaw server access token。
2. 根据 `{bot}` 解析该 bot 的飞书 open_id。
3. 订阅 `FeishuMessageBus`。
4. 只把 mention 到该 bot open_id 的消息以 SSE 推给订阅者。

注意：这不是飞书开放平台 webhook；它是 CSGClaw 内部到 PicoClaw worker 的 bridge。

### 3.4 PicoClaw worker 已有飞书 WebSocket 入站能力

对应代码：

- `/home/jhw/opcsg/picoclaw/pkg/channels/feishu/feishu_64.go`

关键实现：

- `Start()` 中创建飞书 `EventDispatcher`。
- 注册 `OnP2MessageReceiveV1(c.handleMessageReceive)`。
- 创建 `larkws.NewClient(...)`。
- `wsClient.Start(runCtx)` 通过 WebSocket 接收飞书事件。
- `handleMessageReceive(...)` 处理 text/post/interactive/image/file/audio/media 等入站消息。
- 处理群聊 @mention / group trigger。
- 最终调用 `c.HandleMessage(...)` 进入 PicoClaw agent loop。

PicoClaw 文档也明确说明：

> Feishu uses WebSocket/SDK mode and does not use the shared HTTP server.

因此 worker 只要配置了 PicoClaw 的 Feishu app_id/app_secret 并启动 Feishu channel，就可以直接从飞书接收消息。

### 3.5 PicoClaw worker 还支持订阅 CSGClaw Feishu SSE

PicoClaw Feishu channel 在启动时检查 CSGClaw SSE 配置：

- `PICOCLAW_CHANNELS_CSGCLAW_BASE_URL`
- `PICOCLAW_CHANNELS_CSGCLAW_BOT_ID`
- `PICOCLAW_CHANNELS_CSGCLAW_ACCESS_TOKEN`

如果三者都存在，会启动 `runCSGClawSSELoop(...)`，连接：

```text
{base_url}/api/v1/channels/feishu/bots/{bot_id}/events
```

收到 SSE 后，PicoClaw 会把 payload 转成 `larkim.P2MessageReceiveV1`，再走同一个 `handleMessageReceive(...)`。

这说明 CSGClaw 内部飞书消息和真实飞书 WebSocket 消息在 PicoClaw worker 内部最终会汇入同一套处理逻辑。

## 4. 当前不应新增的东西

### 4.1 不应新增“飞书开放平台 webhook callback”作为主路径

旧方案里如果写了“必须新增 webhook endpoint”，需要改掉。

当前 PicoClaw Feishu channel 已经走飞书 WebSocket/SDK 模式。新增 webhook callback 会导致两套入站路径并存，增加配置复杂度：

- WebSocket mode 不需要公网 callback URL。
- 不需要处理飞书 URL verification challenge。
- 不需要用户配置飞书事件订阅 URL。
- 不需要 CSGClaw 暴露公网 endpoint 给飞书。

除非后续明确要支持“PicoClaw 不运行 Feishu WebSocket，由 CSGClaw 统一作为飞书 callback 网关”这种新架构，否则本方案不新增飞书 webhook。

### 4.2 不应把 `/api/v1/channels/feishu/bots/{bot}/events` 误称为飞书 webhook

它是内部 SSE stream，消费者是 PicoClaw worker，不是飞书开放平台。

正确叫法：

- CSGClaw Feishu bot event SSE
- CSGClaw-to-PicoClaw Feishu event bridge
- worker Feishu SSE bridge

## 5. 需要新增或补齐的能力

### 5.1 Channel 配置写入 API/CLI

当前 CSGClaw 可以从主配置读取 `[channels.feishu.<bot_id>]`，但方案需要让 skill 自动写入或更新配置。

当前采用 lite CLI 的 bot config 子命令：

```bash
csgclaw-cli bot config --channel feishu --set \
  --bot-id u-manager \
  --app-id cli_xxx \
  --app-secret-env CSGCLAW_FEISHU_U_MANAGER_APP_SECRET
```

或：

```bash
csgclaw-cli bot config --channel feishu --set \
  --bot-id u-dev \
  --app-id cli_xxx \
  --app-secret-file /path/to/secret-file
```

读取和 reload：

```bash
csgclaw-cli bot config --channel feishu --get --bot-id u-dev
csgclaw-cli bot config --channel feishu --reload
```

要求：

- 不在 stdout/stderr 打印 app_secret。
- 写配置前备份旧配置。
- 支持 idempotent update。
- 支持 `--dry-run` 输出将修改哪些 bot，但不输出 secret。

### 5.2 配置存储位置策略

当前已实现的是主配置内的 `[channels.feishu.<bot_id>]`。

本方案建议先复用现有主配置，避免一次引入新的 loader/saver：

```toml
[channels.feishu.u-manager]
app_id = "cli_xxx"
app_secret = "[REDACTED]"
```

独立配置文件可以作为 Phase 2：

```text
~/.csgclaw/channels/feishu.toml
```

但只有在实现 loader/saver/merge precedence 后才能写入方案主路径。否则文档会误导用户以为独立 channel 配置已经存在。

推荐优先级：

1. Phase 1：复用现有 `~/.csgclaw/config.toml`。
2. Phase 2：增加独立 `~/.csgclaw/channels/feishu.toml`，并明确主配置与独立配置的合并规则。

### 5.3 当前代码如何把飞书 AK/SK 配置到 worker

当前代码不是在 worker 创建后直接修改 PicoClaw 的 `config.json`，也不是让正在运行的 worker 热加载新文件。

当前实现是“创建 PicoClaw sandbox/container 时通过环境变量注入”：

1. CSGClaw server 启动时读取主配置里的 `[channels.feishu.<bot_id>]`。
2. `internal/app/runtimewiring/picoclaw.go` 把 `config.ChannelsConfig` 传给 PicoClaw sandbox runtime。
3. 创建或重建 worker box 时，`BuildRuntimeEnv(...)` 调用 `addFeishuBoxEnvVars(...)`。
4. `addFeishuBoxEnvVars(...)` 按当前 worker 的 `botID` 查 `channels.Feishu[botID]`。
5. 如果查到，就给 sandbox/container 环境变量加入：

```bash
PICOCLAW_CHANNELS_FEISHU_APP_ID=cli_xxx
PICOCLAW_CHANNELS_FEISHU_APP_SECRET=[REDACTED]
```

对应代码：

```go
func addFeishuBoxEnvVars(envVars map[string]string, botID string, channels config.ChannelsConfig) {
    botID = strings.TrimSpace(botID)
    if botID == "" || len(channels.Feishu) == 0 {
        return
    }
    feishu, ok := channels.Feishu[botID]
    if !ok {
        return
    }
    envVars["PICOCLAW_CHANNELS_FEISHU_APP_ID"] = feishu.AppID
    envVars["PICOCLAW_CHANNELS_FEISHU_APP_SECRET"] = feishu.AppSecret
}
```

因此当前的准确说法是：

- CSGClaw 的 `[channels.feishu.<bot_id>]` 是 AK/SK 的来源。
- PicoClaw worker 通过 sandbox/container 环境变量拿到 AK/SK。
- 这些环境变量只在 worker box 创建/重建时生成。
- 当前没有看到“写入 AK/SK 到已运行 worker 的 PicoClaw config.json 并热更新”的实现。

### 5.4 已存在 worker 后再写 AK/SK，如何让 worker 配置更新

如果 worker 已经创建并运行，然后才把 AK/SK 写入 CSGClaw 配置文件，当前代码不会自动把新 AK/SK 注入到已经运行的 worker。

原因有两层：

1. CSGClaw 进程内的 `ChannelsConfig` 是 server 启动时读入并传给 runtime wiring 的。只改磁盘配置文件，不一定会更新当前进程内的 `channels.Feishu`。
2. 即使 CSGClaw 进程内配置已经更新，PicoClaw worker 的 `PICOCLAW_CHANNELS_FEISHU_*` 环境变量也是创建 sandbox/container 时固化进去的；对已运行 box 执行普通 start/stop 不会重新生成环境变量。

当前可行的生效路径应是“通过 `csgclaw-cli bot config --channel feishu --set` 更新配置并 reload -> 重建 worker box”：

```text
写入 ~/.csgclaw/channels/feishu.toml
  -> csgclaw-cli bot config --channel feishu --reload，使 ChannelsConfig 包含 u-dev
  -> 重建 u-dev worker box
  -> 新 box 创建时 BuildRuntimeEnv 注入 PICOCLAW_CHANNELS_FEISHU_APP_ID/APP_SECRET
  -> PicoClaw Feishu WebSocket channel 使用新凭证启动
```

当前代码里用于重建 worker 的能力是后端 API：

```http
POST /api/v1/agents/{id}/recreate
```

重建会重新走 runtime 创建流程，因此会重新执行 `GatewayCreateSpec(...)`、`BuildRuntimeEnv(...)` 和 `addFeishuBoxEnvVars(...)`。

注意：

- `agent stop <id>` + `agent start <id>` 不是等价替代，因为它通常不会重新生成 sandbox/container 环境变量。
- `bot create --channel feishu` 也不是等价替代，因为它只创建/确认 bot/channel 绑定，不负责重建 worker box。
- `csgclaw-cli` 不暴露 agent 命令；skill 中需要重建 worker 时使用后端 recreate API。

Phase 1 建议采用确定性流程：

1. 通过 `csgclaw-cli bot config --channel feishu --set` 写入或更新目标 bot 配置。
2. 默认 `--set` 会触发 reload，也可以显式运行 `csgclaw-cli bot config --channel feishu --reload`。
3. 调用 `POST /api/v1/agents/{id}/recreate` 重建对应 worker box。
4. 通过 masked config 和 worker 日志验证 worker 是否实际拿到了飞书 app_id，并能建立 Feishu WebSocket 连接。

Phase 2 再考虑：

1. 更细粒度的 runtime env refresh 能力，减少完整 recreate。
2. 是否需要给现有 recreate API 增加 CLI 别名；不是 Phase 1 必需项。
3. worker runtime 热更新或安全 recreate 编排。

### 5.5 Feishu bot 选择/创建向导

优先复用 Hermes 已验证的 QR scan-to-create 思路，提供双路径：

路径 A：自动创建新 bot（推荐）

- skill 发起飞书/Lark accounts registration flow。
- 使用 `archetype=PersonalAgent`、`auth_method=client_secret` 获取扫码确认 URL。
- 用户扫码确认后，skill 轮询拿到 `client_id/client_secret`。
- 将 `client_id/client_secret` 映射为 CSGClaw 的 `app_id/app_secret`，并安全写入 channel 配置。

路径 B：手动绑定已有 bot（fallback）

- skill 返回飞书开放平台入口。
- 用户选择已有企业自建应用，确认 Bot 能力已开启并发布。
- 用户安全输入 App ID / App Secret。
- skill 安全写入 channel 配置。

边界：扫码流程用于“创建新 bot 并拿到 secret”；已有应用的 secret 仍不能假设可由 API 读取。

### 5.6 配置验证命令

当前不提供独立 doctor 命令，先通过配置读取和 reload 验证：

```bash
csgclaw-cli bot config --channel feishu --get --bot-id u-manager
csgclaw-cli bot config --channel feishu --get --bot-id u-dev
csgclaw-cli bot config --channel feishu --reload
```

检查项：

- CSGClaw config 中是否存在 `[channels.feishu.<bot_id>]`。
- app_id 是否非空且形如 `cli_...`。
- app_secret 是否存在但不打印。
- 能否调用飞书 bot info API 解析 bot open_id。
- CSGClaw `/api/v1/channels/feishu/bots/{bot}/events` 是否可连接。
- 对 worker：PicoClaw Feishu channel 是否 enabled。
- 对 worker：PicoClaw CSGClaw SSE 配置是否完整。

## 6. 推荐实现分期

### Phase 1：基于现有代码打通自动配置

目标：不改 Feishu 入站架构，不新增飞书 webhook，只补齐配置自动化。

任务：

1. 参考 Hermes `qr_register()`，实现 skill 侧 QR scan-to-create 路径：`init -> begin -> poll -> probe`。
2. 如果 QR 创建失败或用户选择已有 bot，fallback 到手动输入 `app_id/app_secret`。
3. 复用主配置 `[channels.feishu.<bot_id>]` 写入 app_id/app_secret。
4. 创建/更新 CSGClaw bot：
   ```bash
   csgclaw-cli bot create --id u-manager --name manager --role manager --description "your manager agent" --channel feishu
   ```
5. 为 worker runtime 注入 PicoClaw Feishu 配置和 CSGClaw SSE 配置。
6. 配置完成后重启对应 worker。
7. 新增 `doctor` 做最小验证。

验收：

- 用户可通过 skill 配置 manager 飞书 bot。
- 用户可在飞书里和 manager 对话。
- 用户可让 manager 创建 `u-dev` worker 飞书 bot。
- `u-dev` worker 可通过 PicoClaw Feishu WebSocket 接收真实飞书消息。
- 如果 manager 在 CSGClaw 内部房间 @ `u-dev`，`u-dev` 可通过 SSE bridge 收到。

### Phase 2：独立 channel 配置文件

目标：把敏感 channel 凭证从主配置中拆出来。

任务：

1. 增加 loader：读取 `~/.csgclaw/channels/feishu.toml`。
2. 定义 merge precedence：独立 channel 配置是否覆盖主配置。
3. 增加 saver：只更新目标 bot 的 Feishu 配置。
4. `csgclaw-cli bot config --channel feishu --set` 默认写独立配置。
5. 保持兼容旧的 `[channels.feishu.<bot_id>]`。

验收：

- 旧配置仍可使用。
- 新配置可使用。
- 同一个 bot 的重复 set 不破坏其他 bot 配置。
- secret 不进入日志。

### Phase 3：已有飞书 bot 的深度管理

目标：在 Phase 1 已支持“扫码新建 bot”的基础上，进一步减少已有应用接入时的手工步骤。

任务：

1. 研究飞书开放平台是否提供足够 API/OAuth，让用户授权后列出已有应用或校验权限。
2. 如果平台允许安全地管理已有应用，则支持选择已有 bot 并校验配置。
3. 不能假设可以读取已有应用的 `app_secret`；如果拿不到 secret，继续使用手动安全输入。
4. 凭据立即写入 secret store 或配置文件，不在聊天中回显。

验收：

- 用户操作步骤减少。
- skill 不泄露 secret。
- 可重复为 manager/worker 配置不同 bot。

### Phase 4：热更新与运维体验

目标：减少手动 restart。

任务：

1. CSGClaw channel config reload。
2. worker runtime 配置 reload。
3. 前端展示 Feishu bot 配置状态。
4. doctor 页面化。

## 7. 端到端流程设计

### 7.1 配置 manager 飞书 bot

用户在 CSGClaw 前端对 manager 说：

```text
对接飞书机器人
```

manager skill：

1. 检查是否已有 `u-manager` bot。
2. 如果没有，创建：
   ```bash
   csgclaw-cli bot create --id u-manager --name manager --role manager --description "your manager agent" --channel feishu
   ```
3. 引导用户创建/选择飞书 bot。
4. 获取 app_id/app_secret。
5. 写入：
   ```bash
   csgclaw-cli bot config --channel feishu --set --bot-id u-manager --app-id cli_xxx --app-secret-file /secure/path
   ```
6. 验证：
   ```bash
   csgclaw-cli bot config --channel feishu --get --bot-id u-manager
   ```
7. 重启或 reload manager。
8. 告诉用户去飞书里搜索该 bot 并发消息测试。

### 7.2 在飞书里让 manager 创建 worker bot

用户在飞书里对 manager 说：

```text
帮我创建一个 dev 飞书机器人
```

manager skill：

1. 生成 CSGClaw worker id：`u-dev`。
2. 创建/确认 worker：
   ```bash
   csgclaw-cli bot create --id u-dev --name dev --role worker --description "dev worker agent" --channel feishu
   ```
3. 引导用户创建/选择飞书 bot。
4. 获取 app_id/app_secret。
5. 写入 CSGClaw Feishu app config：
   ```bash
   csgclaw-cli bot config --channel feishu --set --bot-id u-dev --app-id cli_xxx --app-secret-file /secure/path
   ```
6. 让 CSGClaw server 重新读取配置。
   - 默认 `--set` 会触发 reload；也可以显式执行 `csgclaw-cli bot config --channel feishu --reload`。
7. 重建 worker box，让 PicoClaw sandbox creation 重新生成环境变量：
   ```http
   POST /api/v1/agents/u-dev/recreate
   ```
8. 新 worker box 创建时，当前 runtime wiring 会从 `[channels.feishu.u-dev]` 自动注入：
   ```bash
   PICOCLAW_CHANNELS_FEISHU_APP_ID=cli_xxx
   PICOCLAW_CHANNELS_FEISHU_APP_SECRET=[REDACTED]
   ```
9. doctor 验证。

## 8. 消息链路

### 8.1 真实飞书到 worker

```text
Feishu/Lark Platform
  -> PicoClaw Feishu WebSocket SDK
  -> OnP2MessageReceiveV1
  -> handleMessageReceive
  -> BaseChannel.HandleMessage
  -> PicoClaw agent loop
  -> worker response
  -> Feishu send message API
```

这条链路不需要 CSGClaw webhook。

### 8.2 CSGClaw 内部飞书消息到 worker

```text
CSGClaw FeishuService.SendMessage
  -> message contains @worker
  -> FeishuMessageBus.Publish(message.created)
  -> GET /api/v1/channels/feishu/bots/{worker}/events
  -> PicoClaw runCSGClawSSELoop
  -> feishuSSEPayloadToLarkEvent
  -> handleMessageReceive
  -> PicoClaw agent loop
```

这条链路使用 CSGClaw 内部 SSE，不是飞书 webhook。

## 9. 安全要求

1. 任何 app_secret、access_token、verification_token、encrypt_key 都不得打印到日志或聊天消息。
2. 文档、测试 fixture、错误信息只使用 `[REDACTED]` 或假值。
3. 写配置前备份旧文件。
4. `doctor` 只显示“存在/缺失/可用/不可用”，不显示 secret 值。
5. skill 在获取 secret 后应立即写入安全位置，聊天上下文只保留 masked 状态。
6. 若支持 `--app-secret-file`，读取后不回显文件内容。

## 10. 验收标准

### 10.1 manager 飞书接入

- `csgclaw-cli bot create --channel feishu` 可创建/确认 manager。
- `csgclaw-cli bot config --channel feishu --set --bot-id u-manager` 可写入配置。
- `csgclaw-cli bot config --channel feishu --get --bot-id u-manager` 返回 masked 配置。
- 用户可在飞书中私聊或 @ manager。
- manager 能回复。

### 10.2 worker 飞书接入

- manager 可创建 `u-dev` worker。
- `u-dev` 有 CSGClaw bot 记录。
- `u-dev` 有 CSGClaw Feishu app config。
- `u-dev` 的 PicoClaw runtime 有 Feishu app_id/app_secret。
- `u-dev` 的 PicoClaw runtime 可选配置 CSGClaw SSE。
- 用户可在飞书里直接和 `u-dev` bot 对话。
- manager 在 CSGClaw/飞书房间 @ `u-dev` 时，`u-dev` 可收到相关消息。

### 10.3 不新增 webhook 的验收

- 方案不要求用户配置飞书开放平台 HTTP callback URL。
- 方案不要求 CSGClaw 暴露公网 webhook。
- PicoClaw Feishu WebSocket mode 可正常启动。
- CSGClaw 内部 SSE endpoint 可正常连接。

## 11. 逐项对齐后的结论

本节把前面遗留的“仍需确认或补齐”逐项落到当前代码事实上，避免 Phase 1 引入不必要的新能力。

### 11.1 `channel feishu set/doctor` CLI

结论：当前没有独立 `channel` 命令，也没有 `channel feishu set/doctor` 子命令。

代码依据：

- `cli/app.go` 当前注册的顶层命令只有 `serve`、`stop`、`agent`、`model`、`user`、`bot`、`room`、`member`、`message`、`completion`、`__complete`、`_serve`。
- `cli/` 目录下也没有 channel 命令包。

Phase 1 已采用 `csgclaw-cli bot config --channel feishu` 统一读写和 reload 配置，skill 不直接编辑配置文件，也不直接调用配置 API。实现要求：

- 不在日志和聊天内容里打印 `app_secret`。
- 只改目标 bot_id 对应配置，不覆盖无关 bot。
- 通过 server 侧 handler 写入独立 Feishu channel 配置并 reload。

```bash
csgclaw-cli bot config --channel feishu --set \
  --bot-id u-dev \
  --app-id cli_xxx \
  --app-secret-file /secure/path

csgclaw-cli bot config --channel feishu --get --bot-id u-dev
csgclaw-cli bot config --channel feishu --reload
```

### 11.2 写配置后如何让运行中的 CSGClaw server 重新读取 `ChannelsConfig`

结论：当前通过 `csgclaw-cli bot config --channel feishu --reload` 触发 Feishu channel 配置 reload。

代码依据：

- `serve` 启动时加载 config，并把 `cfg.Channels` 传入 FeishuService、bot service、agent runtime wiring。
- Feishu config handler 会重新读取主配置和独立 channel 配置文件，并刷新 FeishuService、bot service 和 agent runtime wiring 里的 `ChannelsConfig`。

reload 必须同时刷新这些对象：

1. `FeishuService` 的 app config 映射。
2. bot service 依赖的 channel service。
3. agent runtime wiring 里用于创建 worker env 的 `ChannelsConfig`。

只 reload agent state 不够。已经废弃并删除独立 `csgclaw channel reload` CLI 入口，避免和 `csgclaw-cli bot config --channel feishu --reload` 混淆。

### 11.3 是否需要新增 `agent recreate` CLI

结论：Phase 1 不需要新增。

代码依据：

- 已有后端 `POST /api/v1/agents/{id}/recreate`。
- 已有后端 API 可重建 agent。
- `agent.Service.Create(ctx, req)` 在 `req.Replace == true` 时走 `replace(ctx, req)`。
- 已有测试 `TestCreateReplaceWorkerRecreatesExistingAgent` 覆盖了 replace worker 会删除旧 box 并重新 run 新 box。

Phase 1 推荐复用后端 API：

```http
POST /api/v1/agents/u-dev/recreate
```

服务端以已有 agent 记录为准删除旧 box 并重新创建 worker box，保留已有 agent 的 name、description、image、profile/model 等 metadata。

`agent recreate` CLI 可以作为体验优化，但不能作为 Phase 1 的必要前置。

### 11.4 飞书 app_secret 是否能自动创建或读取

结论：需要拆成两个问题看。

1. 自动创建一个新的飞书/Lark bot，并拿到 `app_id/app_secret`：可以参考 Hermes 的 QR scan-to-create 流程，作为 Phase 1 的优先路径之一。
2. 读取用户已经存在的飞书开放平台应用的 `app_secret`：仍然不能假设可行，应保留手动输入流程。

Hermes 代码依据：

- `/home/jhw/my/hermes-agent/hermes_cli/gateway.py::_setup_feishu()` 提供两种方式：
  - `Scan QR code to create a new bot automatically (recommended)`
  - `Enter existing App ID and App Secret manually`
- QR 路径调用 `/home/jhw/my/hermes-agent/gateway/platforms/feishu.py::qr_register()`。
- `qr_register()` 内部执行 `init -> begin -> poll -> probe`：
  - `_begin_registration()` 调用飞书/Lark accounts registration endpoint：
    - `action=begin`
    - `archetype=PersonalAgent`
    - `auth_method=client_secret`
    - `request_user_info=open_id`
  - `_poll_registration()` 使用 `device_code` 轮询：
    - `action=poll`
    - `tp=ob_app`
  - 成功后返回：
    - `client_id` -> Hermes 保存为 `FEISHU_APP_ID`
    - `client_secret` -> Hermes 保存为 `FEISHU_APP_SECRET`
    - `user_info.open_id`
  - Hermes 随后用 `/open-apis/bot/v3/info` best-effort probe bot identity。
- Hermes 保存逻辑是把 secret 写入本地 `.env`：
  - `save_env_value("FEISHU_APP_ID", app_id)`
  - `save_env_value("FEISHU_APP_SECRET", app_secret)`
  - `save_env_value("FEISHU_DOMAIN", domain)`

因此 CSGClaw 的 Phase 1 可以改为“双路径”：

路径 A：自动创建新 bot（推荐）

1. skill 调用类似 Hermes `qr_register()` 的飞书/Lark registration flow。
2. skill 向用户展示 QR URL 或二维码。
3. 用户扫码确认创建 PersonalAgent。
4. skill poll 到 `client_id/client_secret` 后，把它们映射为 `app_id/app_secret`。
5. skill 通过 `csgclaw-cli bot config --channel feishu --set` 安全写入独立 Feishu channel 配置。
6. 默认 `--set` 触发 reload。
7. 调用 `POST /api/v1/agents/{id}/recreate` 重建 worker box。

路径 B：选择已有 bot（fallback）

1. skill 引导用户进入飞书开放平台控制台。
2. 用户手动提供已有应用的 `app_id/app_secret`。
3. skill 通过 `csgclaw-cli bot config --channel feishu --set` 安全写入独立 Feishu channel 配置。
4. 后续生效流程同路径 A。

边界：

- Hermes 证明“扫码创建新 PersonalAgent 并返回 client_secret”这条链路是可实现的。
- Hermes 并没有证明“任意读取用户已有应用的 app_secret”可行。
- 因此方案不应再把自动创建放到 Phase 2；但也不能把“读取已有应用 secret”写成已具备能力。
- 这条 QR registration flow 依赖飞书/Lark accounts registration 服务，CSGClaw 实现时需要做好失败 fallback：网络失败、用户取消、超时、返回协议变化时，退回手动输入。
- `app_secret` 仍然必须按敏感信息处理：不回显、不进日志、不出现在 shell history，可用 stdin、临时权限文件或直接由进程内写配置完成。

## 12. 修正后的核心判断

你的判断基本是对的：当前代码“能配置飞书”不是空壳，worker 侧 PicoClaw 确实已经有接收飞书消息能力。

但要区分两层配置：

1. CSGClaw 的 `[channels.feishu.<bot_id>]`：让 CSGClaw 知道某个 bot 对应哪个飞书 app，用于发送、解析 bot open_id、SSE mention bridge。
2. PicoClaw worker 的 `channels.feishu`：让 worker 自己通过飞书 WebSocket/SDK 接收真实飞书消息。

所以不需要新增飞书 webhook；真正要补的是自动配置和编排闭环。
