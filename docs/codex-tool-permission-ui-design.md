# Codex 工具调用与权限确认前端化方案

## 1. 目标

当前 Codex runtime 已通过 ACP 接入 `codex-acp`，能够收到工具调用、工具状态更新和权限请求。但现有展示路径仍偏文本化：

- `SessionUpdate.ToolCall` / `ToolCallUpdate` 先归一为 `runtime/codex.SessionEvent`。
- `codexbridge.turnRenderer` 把工具事件渲染为 `Running tool: ...`、`Tool completed: ...` 普通消息。
- `RequestPermission` 会在后端自动选择 `allow_once`，没有 Web UI 确认入口。
- Web UI 的“显示/隐藏工具调用”仍只识别 legacy `🔧 ` 文本前缀。

本方案目标：

1. Codex 工具调用以结构化消息展示，前端可稳定识别并隐藏。
2. Codex 权限请求以 Web 卡片展示，用户点击允许或拒绝后返回 ACP。
3. 第一阶段贴合当前代码，优先复用 `codexbridge`、bot compatibility send、IM message 和 Vite 前端。
4. 借鉴 Matrix instant messaging 的事件形态：事件有 `type`，内容在 `content`，消息子类型由 `content.msgtype` 标识，并提供 `content.body` 作为 fallback。

本方案不把 Matrix 协议引入 CSGClaw，只参考其事件命名和内容信封风格。

## 2. 当前代码判断

### 2.1 Codex runtime 事件源

相关文件：

- `internal/runtime/codex/runtime.go`
- `internal/runtime/codex/session_manager.go`
- `internal/runtime/codex/session_events.go`
- `internal/channel/codexbridge/event_sink.go`

当前链路：

```text
codex-acp
  -> sessionClient.SessionUpdate
  -> runtime/codex.SessionEvent
  -> codexbridge.EventSink
```

`runtime/codex.SessionEvent` 已经覆盖第一阶段需要的事件：

- `text_delta`
- `thought_delta`
- `plan_update`
- `tool_call_start`
- `tool_call_update`
- `permission_request`
- `permission_decision`
- `prompt_completed`
- `prompt_failed`

因此 MVP 不新增跨 runtime activity bus，也不新增 projector 层。

### 2.2 room 上下文只在 codexbridge 里完整

`sessionClient` 只知道 `runtime_id` / `session_id`，不知道当前用户消息来自哪个 IM room。

真正同时握有以下信息的位置是 `internal/channel/codexbridge/bridge.go` 的 prompt 处理循环：

- bot ID / sender ID
- `evt.RoomID`
- `evt.MessageID`
- Codex runtime event stream
- 当前 prompt 是否结束

因此第一阶段的结构化消息投影应放在 `codexbridge`，而不是放在 `runtime/codex` 直接写 IM。

目标路径：

```text
用户消息
  -> IM
  -> im.BotBridge
  -> /api/bots/{id}/events
  -> codexbridge.worker.handleEvent
  -> acp.PromptRequest

Codex 事件
  -> sessionClient
  -> codexbridge.EventSink
  -> codexbridge.worker.handleEvent
  -> /api/bots/{id}/messages/send
  -> IM message
  -> /api/v1/events
  -> Web UI
```

### 2.3 前端真实落点是 web/app

当前前端源代码位于 `web/app/src`，`web/static` 只是 legacy comparison assets。第一阶段应改这些位置：

- `web/app/src/shared/constants/messages.ts`
- `web/app/src/models/conversations.ts`
- `web/app/src/models/agentActivity.ts`
- `web/app/src/components/business/MessageContent/*`
- `web/app/src/api/*`
- `web/app/tests/*`

不要把新实现写进 `web/static/app.js`。

### 2.4 不需要立即扩展 Message kind

当前 bot compatibility send request 只有：

```go
type BotSendMessageRequest struct {
    RoomID string `json:"room_id"`
    Text   string `json:"text"`
}
```

`im.DeliverMessage` 会创建普通 `kind="message"` 的 IM message。为了最小化改动，MVP 不新增 message kind，只把结构化事件 JSON 写入 `message.content`。

后续如果需要服务端查询、更新或审计 activity，再考虑 message metadata、message update 或独立 activity store。

## 3. Matrix 风格事件模型

### 3.1 借鉴原则

Matrix Client-Server API 的 IM 事件有几个值得借鉴的约定：

- room event 有外层 `type` 和内层 `content`。
- 客户端不能假设事件内容总是符合预期 schema，使用前要校验。
- 自定义事件类型应使用反向域名式命名以避免冲突。
- `m.room.message` 通过 `content.msgtype` 区分消息类型。
- 即使客户端不理解某个 `msgtype`，也应有 `content.body` 作为纯文本 fallback。
- `m.notice` 用于自动化客户端响应，并提醒客户端避免自动回复循环。

CSGClaw 采用这些形态，但事件只作为 `im.Message.content` 中的 JSON payload。

### 3.2 顶层事件类型

第一阶段只定义一个 CSGClaw 自定义事件族：

```text
com.opencsg.csgclaw.agent.activity
```

对应 JSON：

```json
{
  "type": "com.opencsg.csgclaw.agent.activity",
  "event_id": "act-...",
  "room_id": "room-...",
  "sender": "u-codex",
  "origin_server_ts": 1779259200000,
  "content": {
    "msgtype": "com.opencsg.csgclaw.agent.tool",
    "body": "Running tool: Run shell command"
  }
}
```

字段说明：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `type` | 是 | 固定为 `com.opencsg.csgclaw.agent.activity` |
| `event_id` | 是 | activity 事件 ID，不要求等于 IM message ID |
| `room_id` | 是 | 当前 IM room ID，由 `codexbridge` 从 bot event 获取 |
| `sender` | 是 | agent/bot user ID |
| `origin_server_ts` | 是 | Unix milliseconds |
| `content` | 是 | activity 具体内容 |
| `unsigned` | 否 | 调试或本地 UI 可用的非关键字段 |

### 3.3 content.msgtype

MVP 只需要这些 `msgtype`：

| `content.msgtype` | 用途 | 来源 |
| --- | --- | --- |
| `com.opencsg.csgclaw.agent.tool` | 工具调用状态 | ACP `ToolCall` / `ToolCallUpdate` |
| `com.opencsg.csgclaw.agent.permission` | 权限请求和决策 | ACP `RequestPermission` |
| `com.opencsg.csgclaw.agent.notice` | Codex 运行错误或 prompt 状态提示 | `prompt_failed` 等 |

不把状态写进 `msgtype`，而是放在具体对象里，例如 `content.tool.status` 或 `content.permission.status`。

### 3.4 工具调用事件

示例：

```json
{
  "type": "com.opencsg.csgclaw.agent.activity",
  "event_id": "act-rt-alice-tool-1-running",
  "room_id": "room-1",
  "sender": "u-alice",
  "origin_server_ts": 1779259200000,
  "content": {
    "msgtype": "com.opencsg.csgclaw.agent.tool",
    "body": "Running tool: Run shell command",
    "runtime": {
      "kind": "codex",
      "runtime_id": "rt-alice",
      "session_id": "sess-1"
    },
    "tool": {
      "id": "tool-1",
      "kind": "execute",
      "title": "Run shell command",
      "status": "running",
      "input_summary": "go test ./internal/runtime/codex"
    }
  }
}
```

工具状态建议：

- `pending`
- `running`
- `completed`
- `failed`
- `canceled`

投影规则：

- `tool_call_start` 生成一条 `tool` activity，状态取 ACP status，空值时按 `running`。
- `tool_call_update` 在状态变化时生成一条 `tool` activity。
- 第一阶段追加消息，不更新已有消息。
- `raw_input` / `raw_output` 默认不展示，只允许脱敏和截断后的摘要进入 `input_summary` / `output_summary`。

### 3.5 权限请求事件

请求示例：

```json
{
  "type": "com.opencsg.csgclaw.agent.activity",
  "event_id": "act-perm-1",
  "room_id": "room-1",
  "sender": "u-alice",
  "origin_server_ts": 1779259200000,
  "content": {
    "msgtype": "com.opencsg.csgclaw.agent.permission",
    "body": "Codex wants permission: Run shell command",
    "runtime": {
      "kind": "codex",
      "runtime_id": "rt-alice",
      "session_id": "sess-1"
    },
    "permission": {
      "id": "perm-1",
      "tool_call_id": "tool-1",
      "title": "Run shell command",
      "status": "pending",
      "requested_at": "2026-05-20T12:00:00Z",
      "expires_at": "2026-05-20T12:01:00Z",
      "options": [
        {
          "id": "once",
          "kind": "allow_once",
          "label": "Allow once"
        },
        {
          "id": "reject",
          "kind": "reject_once",
          "label": "Reject"
        }
      ]
    }
  }
}
```

决策完成后追加一条状态消息：

```json
{
  "type": "com.opencsg.csgclaw.agent.activity",
  "event_id": "act-perm-1-decided",
  "room_id": "room-1",
  "sender": "u-alice",
  "origin_server_ts": 1779259205000,
  "content": {
    "msgtype": "com.opencsg.csgclaw.agent.permission",
    "body": "Permission allowed: Run shell command",
    "runtime": {
      "kind": "codex",
      "runtime_id": "rt-alice",
      "session_id": "sess-1"
    },
    "permission": {
      "id": "perm-1",
      "tool_call_id": "tool-1",
      "title": "Run shell command",
      "status": "allowed",
      "decision": {
        "option_id": "once",
        "kind": "allow_once",
        "decided_at": "2026-05-20T12:00:05Z"
      }
    }
  }
}
```

权限状态建议：

- `pending`
- `allowed`
- `rejected`
- `expired`
- `canceled`

ACP 映射：

- 用户选择 `allow_once` / `allow_always`：返回 `RequestPermissionOutcome.Selected{OptionId}`。
- 用户选择 `reject_once` / `reject_always`：也返回 `Selected{OptionId}`，因为 reject option 是 ACP 提供的合法 option。
- 超时、session 停止、无可用 option：返回 `RequestPermissionOutcome.Cancelled{}`。

## 4. 后端 MVP 设计

### 4.1 新增轻量 PermissionBroker

建议位置：

```text
internal/runtime/codex/permission_broker.go
```

接口草案：

```go
type PermissionBroker interface {
    Request(ctx context.Context, req PendingPermissionRequest) (PermissionDecision, error)
    Decide(ctx context.Context, requestID string, optionID string) (PermissionSnapshot, error)
    Get(requestID string) (PermissionSnapshot, bool)
    CancelSession(runtimeID string, sessionID string)
}
```

行为：

- 生成 `perm-...` 请求 ID。
- 保存 pending request 到内存 map。
- 使用 channel 等待用户决策。
- 超时默认 cancel，不能默认 allow。
- 决策只允许成功一次，重复点击返回当前状态。
- session 停止时取消同 session 下所有 pending request。
- completed cache 短期保留，方便前端重复点击得到稳定响应。

第一阶段只做内存态。服务重启后 pending request 失效，前端旧按钮点击返回 404/410。

### 4.2 改造 sessionClient.RequestPermission

当前逻辑：

```text
publish permission_request
choose allow_once / allow_always
publish permission_decision
return ACP response
```

改造后：

```text
ACP RequestPermission
  -> broker.Request(ctx, normalized request)
      -> publish SessionEventPermissionRequest
      -> wait decision / timeout / cancel
      -> publish SessionEventPermissionDecision
  -> map decision to ACP RequestPermissionResponse
```

`sessionClient` 仍只负责 ACP 边界：

- ACP request -> broker request
- broker decision -> ACP response

pending 状态和幂等由 broker 负责。

### 4.3 codexbridge 负责投影到当前 room

继续使用 `codexbridge.EventSink`。建议在 `internal/channel/codexbridge/render.go` 增加结构化 activity 渲染 helper，在 `bridge.go` 的 `handleEvent` 中按当前 `evt.RoomID` 发送。

简单落地方式：

- `turnRenderer.Apply(event)` 继续处理 `text_delta` 和 `prompt_failed`。
- 从 `turnRenderer.Apply` 移除工具普通文本输出。
- 新增 `renderActivity(event, binding, roomID, senderID)`。
- 对 `tool_call_start` / `tool_call_update` / `permission_request` / `permission_decision` 返回 JSON 字符串。
- `handleEvent` 收到 activity JSON 后调用现有 `sendMessage(ctx, evt.RoomID, jsonText)`。

第一阶段规则：

- `text_delta` 仍由 `turnRenderer` 聚合成普通 assistant message。
- `tool_call_start` / `tool_call_update` 不再发送 `Running tool: ...` 普通文本，而是发送 `com.opencsg.csgclaw.agent.activity` JSON。
- `permission_request` / `permission_decision` 发送 `permission` activity JSON。
- `prompt_failed` 可先保留普通文本，降低风险。

### 4.4 新增 Codex permission API

未抽象 /api/v1/permissions/... 
因为第一阶段只接入 Codex，不使用泛化 API，建议新增明确的 Codex API：

```http
POST /api/v1/codex/permissions/{request_id}/decision
Content-Type: application/json

{
  "option_id": "once"
}
```

响应：

```json
{
  "id": "perm-1",
  "status": "allowed",
  "decision": {
    "option_id": "once",
    "kind": "allow_once",
    "decided_at": "2026-05-20T12:00:05Z"
  }
}
```

错误语义：

- `400`: option 不属于该 request。
- `404`: request 不存在。
- `409`: request 已被决策，响应体带当前状态。
- `410`: request 已过期或所属 session 已取消。

Handler wiring 建议：

- 给 `api.Handler` 增加可选 `codexPermissions PermissionDecider`。
- `server.Options` 增加同名依赖，或在 `newCodexBridgeManager` 创建后注入 handler。
- 不让 API handler 直接理解 ACP 类型，只调用 broker 的窄接口。

### 4.5 wiring 调整

当前 `WithCodexRuntime()` 会创建 `codexbridge.EventSink` 并传给 runtime。

建议调整为：

```text
runtimewiring.WithCodexRuntime()
  -> create codexbridge.EventSink
  -> create runtimecodex.PermissionBroker
  -> runtimecodex.New(... EventSink, PermissionBroker)
```

`serveCodexBridgeManager` 仍负责：

- 找到 Codex runtime。
- 取出 `SessionManager()`。
- 取出 `EventSink()`。
- 启动 `codexbridge.Service`。

需要再提供一个窄接口让 HTTP API 调 `PermissionBroker.Decide`。

## 5. 前端 MVP 设计

### 5.1 parser 与类型

新增常量：

```ts
export const CSGCLAW_AGENT_ACTIVITY_TYPE = "com.opencsg.csgclaw.agent.activity";

export const AgentActivityMsgTypes = {
  tool: "com.opencsg.csgclaw.agent.tool",
  permission: "com.opencsg.csgclaw.agent.permission",
  notice: "com.opencsg.csgclaw.agent.notice",
} as const;
```

建议新增纯模型：

```text
web/app/src/models/agentActivity.ts
```

职责：

- `parseAgentActivity(content)`
- `isAgentActivityPayload(value)`
- `isToolActivityMessage(message)`
- `isPendingPermissionActivity(message)`
- 状态和按钮 label normalization

解析时必须按 Matrix 的安全思路处理：不信任 JSON shape，每个字段都做类型保护和 fallback。

### 5.2 显示/隐藏工具调用

当前调用：

```ts
activeConversation.messages.filter((message) => !isToolCallMessage(message.content))
```

建议改成传整条 message：

```ts
activeConversation.messages.filter((message) => !isToolCallMessage(message))
```

过滤规则：

- legacy `🔧 ` 文本：隐藏。
- `type=com.opencsg.csgclaw.agent.activity` 且 `content.msgtype=...agent.tool`：隐藏。
- pending permission activity：始终显示，因为它需要用户处理。
- permission decision activity：第一阶段也显示，避免刷新后用户看不到结果；后续可加“隐藏已决审批”。

### 5.3 渲染组件

建议在 `MessageContent` 里先识别 agent activity，再走现有 generic structured message：

```text
MessageContent
  -> parseAgentActivity(content)
      -> AgentActivityCard
  -> parseStructuredMessage(content)
      -> ActionCard / StructuredMessageCard
  -> markdown
```

新增组件：

```text
web/app/src/components/business/MessageContent/AgentActivityCard.tsx
```

卡片：

- `ToolActivityCard`
  - title
  - status badge
  - kind
  - input/output summary
- `PermissionActivityCard`
  - title
  - pending buttons
  - status badge for decided/expired/canceled
  - stronger visual treatment for `allow_always`
- `NoticeActivityCard`
  - runtime error/status fallback

按钮点击：

```ts
POST /api/v1/codex/permissions/{request_id}/decision
```

前端本地状态：

- 点击后按钮进入 busy。
- 成功后禁用当前卡片按钮，并等待后端追加 `permission` decision activity。
- 409/410 时显示后端返回状态。

## 6. 分阶段范围

### 6.1 MVP 做

- 只接入 Codex。
- 新增 Codex `PermissionBroker`。
- `RequestPermission` 改为等待 Web 决策或超时 cancel。
- `codexbridge` 把工具调用和权限请求投影成 Matrix 风格 JSON content。
- Web UI 识别并渲染 `com.opencsg.csgclaw.agent.activity`。
- “显示/隐藏工具调用”支持结构化 tool activity 和 legacy `🔧 `。
- pending permission 不受隐藏工具调用影响。
- 测试覆盖 broker、RequestPermission、codexbridge 投影、decision API、前端 parser/卡片。

### 6.2 MVP 不做

- 不新增通用 activity bus。
- 不新增 message kind。
- 不更新同一条 message，只追加状态消息。
- 不做 Feishu/Lark action card。
- 不改 PicoClaw/OpenClaw 工具事件。
- 不展示完整 raw input/output。
- 不做 activity 持久审计索引。

### 6.3 后续再抽象

当 PicoClaw/OpenClaw/Notifier 也有结构化 runtime activity 后，再考虑：

- runtime-neutral event bus
- IM projector
- message update
- activity query API
- channel-specific projector

## 7. 测试建议

后端：

- `internal/runtime/codex`
  - broker pending -> decide -> ACP selected
  - timeout -> ACP cancelled
  - reject option -> ACP selected reject option
  - duplicate decision idempotency
  - session cancel cancels pending requests
- `internal/channel/codexbridge`
  - tool start/update sends JSON activity to current room
  - permission request sends pending card JSON
  - text delta still aggregates into ordinary assistant message
  - tool events no longer produce `Running tool: ...` plain text
- `internal/api`
  - decision endpoint success
  - invalid option
  - expired/missing request

前端：

- `web/app/tests/models`
  - parse valid agent activity
  - reject invalid shapes safely
  - tool filtering
  - pending permission remains visible
- `web/app/tests/components/MessageContent`
  - renders tool activity card
  - renders permission buttons
  - calls decision API callback
  - displays busy/error/decided states

验证：

```bash
go test ./internal/runtime/codex ./internal/channel/codexbridge ./internal/api
pnpm --dir web/app test
pnpm --dir web/app typecheck
```

## 8. 安全边界

- 权限确认不能通过普通聊天文本解析，必须走专门 API。
- `allow_always` 必须明显区别于 `allow_once`。
- 超时默认 cancel/reject，不能默认 allow。
- raw input/output 需要截断和脱敏后才能进入前端 payload。
- 前端必须把 activity JSON 视为不可信输入，不能直接渲染 HTML。
- 决策 API 第一阶段是本地 UI 能力，不应声称提供强用户身份审计。

## 9. 参考

- Matrix Client-Server API v1.18: Instant Messaging
  - 自定义事件类型建议使用 namespaced type。
  - room event 使用 `type` + `content`。
  - `m.room.message` 使用 `content.msgtype` 和 fallback `content.body`。
  - `m.notice` 适合自动化客户端通知。
- `docs/web/development.md`
- `internal/runtime/codex/session_manager.go`
- `internal/channel/codexbridge/bridge.go`
- `internal/channel/codexbridge/render.go`
- `web/app/src/components/business/MessageContent/structuredMessages.ts`
