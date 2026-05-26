# Codex 对接飞书设计说明

本文说明现有 PicoClaw 如何对接飞书，以及在 CSGClaw 中为 Codex runtime 增加飞书通道时推荐的模块边界、消息流和接口映射。

## 背景

CSGClaw 目前同时支持 PicoClaw runtime 和 Codex runtime。PicoClaw 已经可以接入飞书群聊，但它的飞书连接发生在 PicoClaw runtime 内部；Codex runtime 目前通过 ACP 与 CSGClaw 交互，并不直接连接飞书。

推荐方案是：由 CSGClaw server 作为 Codex 与飞书之间的适配层，一边连接飞书 WebSocket/REST API，一边通过现有 `codexbridge` 将消息转换为 Codex ACP prompt。

## 现有 PicoClaw 飞书接入

PicoClaw 的飞书接入是 runtime 自己完成的：

```text
飞书
  <-> PicoClaw Feishu Channel
  <-> PicoClaw MessageBus
  <-> PicoClaw AgentLoop
  <-> CSGClaw LLM/API
```

### 入站消息流

```text
飞书群聊 / 私聊
  -> 飞书 WebSocket 推送 message event
  -> picoclaw/pkg/channels/feishu
  -> 转成 bus.InboundMessage
  -> MessageBus.PublishInbound
  -> AgentLoop 消费 InboundChan
  -> AgentLoop 处理消息
```

关键模块：

| 职责 | 模块 |
| --- | --- |
| 飞书通道实现 | `picoclaw/pkg/channels/feishu/feishu_64.go` |
| 建立飞书 WebSocket | `FeishuChannel.Start` |
| 注册飞书消息事件 | `OnP2MessageReceiveV1(c.handleMessageReceive)` |
| 通道通用接口 | `picoclaw/pkg/channels/base.go` |
| 消息总线 | `picoclaw/pkg/bus/bus.go` |
| Agent 主循环 | `picoclaw/pkg/agent/loop.go` |

### 出站消息流

```text
AgentLoop
  -> MessageBus.PublishOutbound
  -> ChannelManager.dispatchOutbound
  -> FeishuChannel.Send
  -> 飞书 REST API message create
  -> 飞书群聊 / 私聊
```

PicoClaw 的 `FeishuChannel.Send` 会优先发送 interactive card，失败时回退为文本消息。

### 配置流

飞书凭证由 CSGClaw 管理，然后在启动 PicoClaw sandbox runtime 时注入环境变量：

```text
CSGClaw channels/feishu.toml
  -> feishu.Provider.BotConfig(botID)
  -> runtimewiring.addFeishuBoxEnvVars
  -> PicoClaw env
```

注入的关键环境变量：

```text
PICOCLAW_CHANNELS_FEISHU_APP_ID
PICOCLAW_CHANNELS_FEISHU_APP_SECRET
```

PicoClaw 读取这些环境变量后，自己创建飞书 SDK client 和 WebSocket client。因此，PicoClaw 并不是通过 CSGClaw server 的 HTTP/SSE 接口来收发飞书群聊消息。

## CSGClaw 当前飞书能力

CSGClaw server 目前已有飞书 REST 能力，主要用于配置和管理操作：

```text
CSGClaw server
  -> 飞书 REST API
```

已有能力包括：

- 保存和加载飞书 bot 配置
- 获取 bot 信息
- 创建群聊
- 添加群成员
- 拉取群成员和群消息
- 发送管理类消息

主要模块：

| 职责 | 模块 |
| --- | --- |
| 飞书配置文件 | `internal/channel/feishu/store.go` |
| 飞书配置 provider | `internal/channel/feishu/provider.go` |
| 飞书 REST service | `internal/channel/feishu/service.go` |
| 飞书 API handler | `internal/api/feishu.go` |
| 飞书配置 API handler | `internal/api/feishu_config.go` |

当前缺少的是：

```text
飞书 WebSocket
  -> CSGClaw server
```

也就是说，CSGClaw server 当前不是飞书实时群聊消息入口。

## Codex 对接飞书推荐方案

Codex 不直接连接飞书。推荐由 CSGClaw server 代表 Codex 连接飞书：

```text
飞书
  <-> CSGClaw FeishuCodexClient
  <-> CSGClaw codexbridge
  <-> Codex ACP runtime
```

### 入站消息流

```text
飞书群聊 / 私聊
  -> 飞书 WebSocket 推送 message event
  -> CSGClaw FeishuCodexClient.StreamEvents
  -> 转成 codexbridge.BotEvent
  -> codexbridge.Service
  -> 转成 ACP PromptRequest
  -> Codex runtime
```

### 出站消息流

```text
Codex runtime
  -> ACP session events
  -> codexbridge.Service
  -> FeishuCodexClient.SendMessage
  -> 飞书 REST API message create
  -> 飞书群聊 / 私聊
```

### 推荐新增模块

建议新增：

```text
internal/channel/feishu/codexclient
```

该模块实现现有的 `codexbridge.BotClient` 接口：

```go
type BotClient interface {
    StreamEvents(ctx context.Context, botID, lastEventID string) (<-chan BotEvent, <-chan error)
    SendMessage(ctx context.Context, botID string, req SendMessageRequest) (SendMessageResponse, error)
}
```

其中：

```text
StreamEvents
  = 飞书 WebSocket 入站
  = 飞书 message event -> codexbridge.BotEvent

SendMessage
  = 飞书 REST 出站
  = codexbridge.SendMessageRequest -> 飞书 message create
```

### 事件映射

飞书事件应映射为 `codexbridge.BotEvent`：

| Feishu 字段 | Codex bridge 字段 |
| --- | --- |
| `message_id` | `MessageID` |
| `chat_id` | `RoomID` |
| chat type | `ChatType` |
| text content | `Text` |
| mentions | `Mentions` |
| thread/root message | `ThreadRootID` / `ThreadContext` |

群聊中建议沿用 PicoClaw 的处理语义：

- 支持只响应 @bot 的消息
- 收到消息后去除 bot mention
- 使用 bot open_id 做可靠 mention 判断
- 对重复事件做去重

### 出站映射

`codexbridge.SendMessageRequest` 应映射到飞书消息发送：

| Codex bridge 字段 | Feishu 字段 |
| --- | --- |
| `RoomID` | `receive_id`，类型为 `chat_id` |
| `Text` | message content |
| `ThreadRootID` | thread/reply 目标，按飞书能力补齐 |
| `MessageID` | activity/update 场景可映射为编辑或补充消息 |

第一版可以只支持文本回复；后续再补 interactive card、消息编辑、占位消息和 reasoning/activity 消息。

## 与 PicoClaw 方案的对齐关系

```text
PicoClaw:
飞书 WS -> PicoClaw Feishu Channel -> MessageBus -> AgentLoop -> 飞书 REST

Codex:
飞书 WS -> CSGClaw FeishuCodexClient -> CodexBridge -> ACP -> 飞书 REST
```

| 层面 | PicoClaw | Codex 推荐方案 |
| --- | --- | --- |
| 飞书入站 | PicoClaw runtime 连接 WebSocket | CSGClaw server 连接 WebSocket |
| 飞书出站 | PicoClaw runtime 调 REST | CSGClaw server 调 REST |
| 消息抽象 | `bus.InboundMessage` | `codexbridge.BotEvent` |
| 回复抽象 | `bus.OutboundMessage` | `codexbridge.SendMessageRequest` |
| Agent 执行 | PicoClaw `AgentLoop` | Codex ACP runtime |
| 配置来源 | CSGClaw `channels/feishu.toml` | CSGClaw `channels/feishu.toml` |
| 凭证使用方 | PicoClaw runtime | CSGClaw server |

两套方案在飞书协议层面对齐：都是 WebSocket 入站、REST 出站。差异在 runtime 边界：PicoClaw 自己持有飞书连接；Codex 由 CSGClaw server 持有飞书连接。

## 为什么不放到 Codex 插件里

Codex 插件或 MCP server 更适合作为会话内工具，例如查询飞书消息、发送飞书消息、查询用户信息等。它们不适合作为飞书 WebSocket 的常驻入站网关，因为飞书事件到达时仍然需要有一个外部进程接住事件并触发 Codex turn。

因此，推荐：

```text
飞书入站和出站适配：放在 CSGClaw server
Codex 推理和执行：继续走 ACP
可选飞书工具能力：后续再通过 MCP/插件暴露给 Codex
```

## 实施步骤

1. 新增 `internal/channel/feishu/codexclient`，实现 `codexbridge.BotClient`。
2. 复用 `feishu.Provider.BotConfig(botID)` 获取 `app_id` 和 `app_secret`。
3. 在 `StreamEvents` 中为 bot 启动飞书 WebSocket client。
4. 将飞书消息事件转换为 `codexbridge.BotEvent`。
5. 在 `SendMessage` 中调用飞书 REST message create API。
6. 修改 Codex bridge manager，根据 agent/channel 选择 bot client。
7. 为配置 reload、WebSocket 重连、消息去重和群聊 mention 过滤补测试。

## 最终结论

PicoClaw 飞书接入是 runtime 自己连飞书；Codex 飞书接入建议由 CSGClaw server 代 Codex 连飞书。

这样可以复用现有 Codex ACP bridge，不需要修改 Codex runtime，也不需要新增一套 Codex 专用 HTTP API。CSGClaw server 内部新增 Feishu-backed `BotClient` 即可完成协议适配。
