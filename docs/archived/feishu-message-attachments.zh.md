# 飞书消息附件传递到 Agent Runtime 方案

## 背景与目标

Issue 2408 的场景是：用户在飞书群里给 Git 助手 `dev` 发送图片，希望 Agent 把图片评论到 GitLab Issue。当前 Agent 只能看到飞书图片的 `image_key`，例如：

```text
img_v3_0212f_73498d91-ebe2-46af-8d22-f2da12fdcbag
```

Agent 在自己的沙箱/工作区中找不到本地图片文件，也拿不到可下载 URL，因此不能把图片上传到 GitLab。

本方案目标：

- 飞书入站图片由接收飞书消息的 Agent runtime 下载或解析成真实文件。
- Agent 能拿到当前 runtime 可访问的本地文件路径；`media://` 可以继续作为 runtime 内部引用，但不能作为 GitLab 等外部工具唯一输入。
- 图片既可以作为模型多模态输入，也必须在 Agent 文本上下文中暴露工具可读路径，例如 `[image:/tmp/picoclaw_media/...]`。
- CSGClaw 只消费 runtime 产生的文本、活动消息和媒体描述，不重复下载飞书资源。
- Agent 能稳定把本地图片交给 GitLab API 上传，或生成 Markdown 图片链接。

## 当前代码判断

当前问题只按 **runtime-owned Feishu 入站** 处理。按当前 PicoClaw 代码，飞书入站连接是 Lark SDK WebSocket，而不是 SSE。这里的“下载/登记媒体”不是 CSGClaw 服务端新增能力，而是由 PicoClaw 自己的 Feishu channel 完成：

```text
Feishu/Lark
--WebSocket--> PicoClaw runtime Feishu channel
-> runtime-local media handler 下载或登记附件
-> Agent prompt/tool context 中出现 runtime 可访问路径或 media ref
-> Agent 上传 GitLab / 生成评论
-> Agent 输出再通过 CSGClaw bridge/API 或 csgclaw-cli 回到 CSGClaw/飞书
```

也就是说，飞书消息先到 Agent runtime，然后才通过 Agent 的输出或活动消息回到 CSGClaw。PicoClaw 如果使用 `csgclaw-cli message create --channel feishu` 或 CSGClaw API 发回飞书，那属于出站/回写路径：CSGClaw 再调用飞书 OpenAPI 发消息。它不携带飞书原始 `post` / `image` 事件，也不作为附件下载或 materialization 的主链路。

participant SSE 是 CSGClaw 给 runtime、Agent 或 Web UI 的内部 IM 事件桥接和重放机制；飞书 `message_id`、`image_key/file_key`、raw content 等原始附件上下文来自 PicoClaw Feishu WebSocket 事件。CSGClaw 的职责是把 Feishu channel 配置正确注入 runtime、确保运行的 runtime 镜像包含对应能力，并展示 runtime 返回的文本或媒体摘要。

PicoClaw 本身已有部分 Feishu 图片处理能力，是 runtime-owned 模式：

1. PicoClaw gateway 创建 `MediaStore`，注入 channel 和 agent loop。
2. Feishu channel 收到消息后，由 runtime 内部调用消息资源接口下载媒体。
3. 图片消息使用 `image_key` 搭配 `message_id` 下载；文件、音频、视频使用对应文件资源 key。
4. 下载文件写到 runtime-local media temp dir，典型路径是 `/tmp/picoclaw_media/...`。
5. `FileMediaStore.Store(...)` 登记本地文件并返回 `media://<uuid>`。
6. Agent loop 在进入 provider 前 resolve media refs：图片可转成多模态输入，非图片可把本地路径注入文本内容。

因此，PicoClaw 不是缺少“用 `message_id + image_key` 下载飞书图片”的底座能力。当前已有 `downloadResource(ctx, messageID, imageKey, "image", ".jpg", store, scope)` 这条路径，会调用飞书 message resource 接口，把资源写入 `/tmp/picoclaw_media/...` 并登记为 `media://<uuid>`。

当前需要补齐两个小闭环：

1. 这条下载路径只被部分消息类型调用，`post` 富文本图片还没有接上。
2. 已下载的图片目前主要作为模型视觉输入解析，不能保证 GitLab 上传等工具能从 prompt 中拿到本地路径；需要额外暴露图片本地路径或提供等价 resolver。

| 飞书消息类型 | 当前 PicoClaw 行为 | 本方案要求 |
|---|---|---|
| `image` | 从 content 顶层 `image_key` 下载图片并登记 `media://<uuid>` | 保持 |
| `interactive` | 递归提取卡片图片 key 或外部 URL | 保持 |
| `file` / `audio` / `media` | 用 `file_key` 下载并登记 | 保持 |
| `post` | 直接把富文本 raw JSON 传给 Agent，不下载其中的图片 | 必须提取富文本图片并下载 |

当前部署里，用户在飞书发送“图片 + 文字”时，PicoClaw 收到的是 Feishu 原生 `post` 富文本消息，不是 CSGClaw bridge 文本消息。日志里可见消息已经进入 PicoClaw `feishu` channel，content 形态类似：

```json
{
  "title": "",
  "content": [
    [
      {
        "tag": "img",
        "image_key": "img_v3_0212f_73498d91-ebe2-46af-8d22-f2da12fdcbag",
        "width": 670,
        "height": 106
      }
    ],
    [
      {
        "tag": "text",
        "text": "将这个图片评论在issue中并@ 季宏伟"
      }
    ]
  ]
}
```

根因链路是：

```text
Feishu/Lark 用户发送图片+文字
-> PicoClaw Feishu WebSocket 收到 msg_type=post
-> PicoClaw extractContent(post) 直接返回 raw JSON
-> downloadInboundMedia 没有 MsgTypePost 分支
-> raw JSON 中的 image_key 进入 Agent prompt
-> Agent 只能看到 image_key，找不到本地文件或 media:// 引用
```

因此，如果某个部署中 Agent 仍只能看到 `image_key`，优先检查消息是否是 `post` 富文本图片、运行中的 PicoClaw runtime 是否包含 `MsgTypePost` 图片下载能力、Feishu channel 是否启用、权限是否足够，以及媒体路径是否被注入给 Agent，而不是在 CSGClaw 服务端补一套下载链路。

因此，这个问题的根因不是 GitLab comment 工具缺失，也不是 CSGClaw 的 Feishu channel 缺少下载器，而是 PicoClaw runtime-owned 飞书入站缺少 `post` 富文本图片的 materialization，并且图片路径还没有稳定暴露给工具调用：它没有把富文本里的 `image_key` 稳定转换成 Agent 可读的本地路径或可解析引用。

## 架构原则

### 1. 入站所有权在 runtime

飞书消息由 PicoClaw runtime 直接接收时，CSGClaw 不应再抢占下载动作。原因是：

- runtime 才拥有这条飞书入站消息的完整上下文，包括 message id、resource key、app config 和当前会话。
- Agent 读取文件发生在 runtime 内，只有 runtime 能判断路径是否对 Agent 可见。
- CSGClaw 服务端 host path 不一定挂载到 Agent sandbox，直接给 Agent host cache path 没有意义。
- CSGClaw 不应把飞书 app secret 或 tenant token 传给 Agent。



### 2.路径必须是 Agent 视角

GitLab 上传需要真实文件。仅把图片作为模型视觉输入不够，因为工具调用或 shell 还需要能读取该文件。

不同 runtime 的 Agent 可见路径应由 runtime 自己给出：

| runtime | runtime-owned 媒体形态 | 处理原则 |
|---|---|---|
| PicoClaw sandbox | `/tmp/picoclaw_media/...`，`media://<uuid>` | 视为 PicoClaw MediaStore 管理路径或引用；由 PicoClaw resolver/tool 负责读取 |

如果 runtime 选择把附件复制到 workspace，也必须由 runtime 根据自己的 mount 规则生成路径。CSGClaw 不在 Feishu channel 层硬编码 workspace 表。

PicoClaw 的最小实现建议是不新增服务、不复制到 workspace：在现有 `resolveMediaRefs` 中保留图片转 data URL 的模型输入能力，同时把图片本地路径追加到同一条用户消息，例如：

```text
将这个图片评论在 issue 中 [image:/tmp/picoclaw_media/msg-img.jpg]
```

这样模型仍能看图，`read_file`、`exec`、GitLab 上传工具也能读取同一个 runtime-local 文件。非图片附件继续使用现有 `[file:/path]`、`[audio:/path]`、`[video:/path]` 形式。

还需要覆盖飞书常见的分开发送场景：用户先发送一张图片，再发送一句“把这个图片评论到 issue”。这两条飞书消息有不同的 `message_id`，第二条文本消息不会携带第一条图片的 `mediaRefs`。因此图片路径标签不能只存在于当前 provider 请求的临时消息里，必须进入 PicoClaw session history：

```text
第一条图片消息进入历史:
[image: photo] [image:/tmp/picoclaw_media/msg-img.jpg]

第二条文本消息:
把这个图片评论到 issue
```

这样 Agent 在处理第二条文本指令时，可以从最近历史中找到上一张图的本地路径，而不需要用户手动补充路径。

### 3 失败必须对 Agent 可见

资源读取失败时不能只留下 `image_key`。runtime 需要把失败状态注入 Agent 可见上下文，例如：

```text
Attachments:
- image img_v3_xxx: unavailable (read feishu image failed: missing permission)
```

失败摘要不能包含 app secret、tenant token、Authorization header 或完整敏感响应体。

### 4重连和重放不能重复副作用

飞书入站这条链路的重连/重投来自 Lark WebSocket、PicoClaw runtime retry 或飞书事件重投。CSGClaw participant SSE reconnect 只会从 CSGClaw IM 历史重新构造内部事件，不应作为飞书原始附件下载的触发源。虽然本方案不在 CSGClaw 侧下载附件，但 runtime 侧仍需要保证飞书入站媒体处理是幂等的：

- 同一条飞书 `message_id + image_key/file_key` 重复到达时，复用已有 media record 或 deterministic path。
- 建议幂等键固定为 `message_id + resource_type + image_key/file_key`；图片富文本场景中 `resource_type=image`，资源 key 是 `content[*][*].image_key`。
- 不因为 Feishu WebSocket 重投、runtime retry、Agent retry 或 CSGClaw participant SSE replay 的误触发而重复下载并生成多个不同路径。
- 如果 media temp dir 有 TTL，Agent prompt 中应避免引用已经被清理的路径；必要时 runtime resolver 应能重新 materialize。

为了保持改动简单，第一版不需要新增完整的持久化 media registry。可以先利用现有本地文件名 `message_id + resource_key + ext`，并让 `FileMediaStore.Store(localPath, scope)` 在同一 `scope + localPath` 已登记时返回已有 ref，而不是每次生成新的 `media://<uuid>`。如果后续需要跨进程、跨重启幂等，再升级为显式 deterministic key。

## Runtime 实现要求

### PicoClaw

PicoClaw 保持 MediaStore 模式：

1. Feishu channel 下载图片到 runtime-local media temp dir。
2. `FileMediaStore.Store(...)` 返回 `media://<uuid>`。
3. Agent loop 在进入 provider 前 resolve media refs。
4. 对 GitLab 上传这类需要真实文件的动作，Agent 必须能通过 resolver/tool 拿到本地文件路径或字节流。

PicoClaw 需要把 `post` 富文本作为一等入站附件来源处理：

1. 对 `msg_type=post` 解析 `content` 二维数组。
2. 提取所有 `tag="img"` 元素中的 `image_key`。
3. 使用当前消息的 Feishu `message_id`，复用已有 message resource 下载接口。
4. 下载结果走现有 MediaStore：写入 `/tmp/picoclaw_media/...`，登记为 `media://<uuid>`。
5. 将富文本里的 `text` 元素 flatten 成普通文本；不要把 raw JSON 作为 Agent 主体内容再追加 `[attachment]`，避免破坏 JSON 语义。
6. 下载失败时把失败原因作为 Agent 可见摘要注入，而不是只留下 `image_key`。

具体代码接点应在 PicoClaw runtime 内完成：

1. 在 Feishu content helper 中增加 `post` 富文本图片提取函数，输入 raw content，输出 `[]image_key`。
2. 在 Feishu content helper 中增加 `post` 富文本文字提取函数，输入 raw content，输出普通文本。
3. 在 `downloadInboundMedia` 增加 `MsgTypePost` 分支。
4. `MsgTypePost` 的 Agent 主体内容使用 flatten 后的文本，而不是 raw JSON。
5. `MsgTypePost` 分支复用现有 `downloadResource(ctx, messageID, imageKey, "image", ".jpg", store, scope)`。
6. 下载得到的 refs 继续走现有 `mediaRefs -> appendMediaTags -> HandleMessage(..., mediaRefs, ...)` 链路；`interactive` 这类仍保持 raw JSON 的消息继续跳过 `appendMediaTags`。
7. 在 Agent media resolver 中对图片额外注入 `[image:<localPath>]`，不要只生成 data URL。
8. 注入后的图片路径标签必须写入 session history；不能只存在于当前 provider 请求里。
9. 下载失败时生成脱敏失败摘要并追加到 Agent 可见内容。
10. 本次不是重写 Feishu 图片下载器，而是把 `post.content[*][*].tag="img"` 中的 `image_key` 接入现有下载器。
11. 不新增 CSGClaw 服务端下载接口，也不把飞书 token、app secret 或 host path 暴露给 Agent。

PicoClaw 不需要把附件固定复制到 `~/.picoclaw/workspace/attachments/...`。如果后续为了工具兼容选择复制到 workspace，应作为 PicoClaw runtime 自己的 materialization 策略，而不是 CSGClaw Feishu channel 的职责。

## CSGClaw 边界

本方案中 CSGClaw 侧只做这些事情：

- 写入或更新 runtime 的 Feishu channel 配置。
- 接收 Agent/runtime 产生的文本、活动消息或媒体描述。
- 在 IM 中展示 runtime 返回的附件摘要或失败摘要。
- 保持日志脱敏，不打印飞书 app secret、tenant token 或完整 Authorization header。

本方案中 CSGClaw 侧不做这些事情：

- 不增加服务端飞书图片下载主链路。
- 不增加服务端附件补下载或解析接口。
- 不在 IM 投递层注入 Feishu resource resolver。
- 不把 host-only path 写入 `apitypes.Message`、SSE data、prompt 或飞书回包。
- 不把 PicoClaw runtime-local media store 搬进 `internal/im`。

## 安全策略

- 飞书资源下载只在 runtime 内使用对应 app config，CSGClaw 不复制 app secret 给 Agent。
- 文件名必须安全化，禁止路径穿越。
- 默认限制下载大小，超限时返回可见失败。
- 图片必须校验 magic bytes 或 `http.DetectContentType` 结果。
- 不下载表情包、合并转发子消息等当前 runtime 不支持的资源。
- runtime-local 媒体遵循对应 runtime 生命周期，例如 PicoClaw `MediaStore` TTL 清理。
- 清空 CSGClaw IM 聊天记录不应被理解为删除 runtime media 或 Agent workspace 文件。

## 实现步骤

### 阶段 1：修正 runtime 入站附件处理

1. 确认 PicoClaw Feishu 入站消息能拿到 `message_id + image_key/file_key`。
2. 增加 `post` 富文本图片解析：从 `content[*][*]` 中提取 `tag="img"` 的 `image_key`。
3. 增加 `post` 富文本文字 flatten：从 `content[*][*]` 中提取 `tag="text"` 的文本，作为 Agent 主体内容。
4. 对 `image`、`post`、`interactive`、`file/audio/media` 统一走 runtime 内下载、大小限制、内容校验和安全文件名处理。
5. 生成 Agent 可访问的 runtime path 和 `media://` 引用；图片保留多模态输入，同时追加 `[image:<localPath>]`。
6. 将追加了 `[image:<localPath>]` 的内容写入 session history，保证图片和文本分开发送时后续文本指令仍能引用上一张图。
7. 下载失败时生成 Agent 可见失败摘要。
8. 对同一 `scope + localPath` 复用已有 media ref，避免重复事件生成多个无关 `media://`。

需要补充的 PicoClaw 单测：

1. `post` content fixture 能提取一个或多个 `tag="img"` 的 `image_key`。
2. `post` content fixture 能提取文字内容，最终 Agent 主体内容不再是 raw JSON。
3. `downloadInboundMedia(MsgTypePost)` 会对每个图片 key 调用 message resource 下载并返回 `media://` refs。
4. `post` 消息中的文本内容仍保留，最终 Agent 输入包含文字、图片路径标签和附件摘要或 media ref。
5. 单独图片消息写入 session history 时包含 `[image:<localPath>]`，下一条纯文本指令能从历史找到该路径。
6. 下载失败时消息仍投递给 Agent，并包含脱敏失败摘要，不泄露 token、app secret 或 Authorization header。
7. 同一 `message_id + image_key` 重复处理时不会生成多个无关本地文件或重复 media record。

### 阶段 2：Agent 输入和工具可读性

1. prompt 或 runtime message metadata 中包含附件摘要。
2. Agent shell/tool 能读取 runtime path；图片场景推荐在 prompt 和 session history 中都出现 `[image:<localPath>]`。
3. GitLab 上传工具使用真实文件路径或字节流，而不是飞书 `image_key`。
4. 当图片和文字分开发送时，后续文本指令能引用最近一条图片消息中的本地路径。

### 阶段 3：幂等和生命周期

1. 对同一 `message_id + file_key/image_key` 做幂等处理。
2. runtime retry/reconnect 不重复生成无关路径。
3. media temp dir 清理策略不会让刚注入 prompt 的路径立刻失效。
4. 日志中保留 message id、resource key hash、runtime path/status 等排障信息，但不泄露 token。

### 阶段 4：文档和验证

1. 更新 runtime Feishu skill 文档，提示用户发送图片后 Agent 应看到 `Attachments:` 路径或 media ref。
2. 如果飞书下载需要额外 scope，runtime registration/finalize 输出中补充权限提示。
3. 保留 CSGClaw 文档中的边界说明：当前方案不考虑 CSGClaw 服务端附件下载。

## 验收标准

1. 飞书单独图片消息：PicoClaw Feishu channel 下载图片到 `picoclaw_media`，生成 `media://<uuid>`，Agent loop 能 resolve。
2. 飞书“图片 + 文字”富文本 `post` 消息：PicoClaw 提取 `content[*][*].image_key` 和文本内容，下载图片并生成 `media://<uuid>`。
3. Agent 输入同时包含富文本文字、图片多模态输入和 `[image:<localPath>]`。
4. 飞书先发送单独图片、再发送文本指令时，第二条文本指令能从 session history 中找到上一张图片的 `[image:<localPath>]`。
5. `dev` 在自己的 shell/tool 中可以读取该路径，或通过 runtime media resolver 读取真实文件。
6. Agent 能把该图片上传到 GitLab Issue #2408，并生成评论 Markdown。
7. 飞书图片下载失败时，Agent 收到明确失败原因，不再只看到 `image_key`。
8. 同一飞书消息重复投递时，不生成多个无关本地文件或多个无关 media ref。
9. CSGClaw IM 只展示 runtime 返回的文本、媒体摘要或失败摘要，不出现 host-only path、app secret、tenant access token。
