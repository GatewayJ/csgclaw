# Feishu participant 接入与配置迁移设计草案

本文档记录当前 Feishu 接入失败的根因、目标架构和拟议修改范围，便于后续讨论后再落代码。

## 背景

近期 participant 架构改造后，CSGClaw 开始把“运行时 agent 身份”和“渠道 participant 身份”拆开。这个方向是对的，但 Feishu 接入里还残留了旧的 `bot_id` 语义，导致已有 worker 重新对接 Feishu 时可能无法注入 Feishu 运行时配置。

现场现象可以概括为：

- manager 能回复 Feishu，因为它的 agent ID、Feishu 配置 key、Feishu participant ID 刚好都对齐。
- 新建 worker 有时能回复，因为创建流程会让 Feishu 配置 key 和新 agent ID 恰好一致。
- 已有 worker 重新绑定 Feishu 时容易失败，因为 Feishu 配置 key 使用了新传入的 `bot_id`，但运行时仍按已有 agent ID 查询配置。

## ID 语义

### agent ID

agent ID 是运行时 agent 的身份，用于定位 agent、容器和 profile。

当前新架构倾向于：

- agent ID 带 `u-` 前缀，例如 `u-agent-zcnnq5`、`u-manager`。
- agent ID 不应该被当作某个渠道的配置 key。

### participant ID

participant ID 是某个渠道里的本地参与者身份。

一个 agent 可以绑定多个 participant，例如：

```text
feishu participant A -> agent X
csgclaw participant B -> agent X
telegram participant C -> agent X
```

因此 Feishu 的运行时配置应该挂在 Feishu participant 上，而不是挂在 agent ID 上。

当前代码里，自动生成 agent participant ID 时已经有去掉 `u-` 前缀的倾向；但如果显式传入 participant ID，代码会尊重传入值，所以历史数据里仍可能存在带 `u-` 的 Feishu participant。

### bot_id

`bot_id` 不是 Feishu 官方 ID。

Feishu 官方 ID 更接近这些概念：

- `app_id`，例如 `cli_xxx`
- `open_id`、`union_id` 等 Feishu 用户身份

CSGClaw 当前的 `bot_id` 实际上只是本地 `channels/feishu.toml` 里的配置 key：

```toml
[bots.some-key]
app_id = "cli_xxx"
app_secret = "..."
```

因此后续设计里应废弃 `bot_id` 这个业务语义，把它明确替换为 Feishu participant ID。对外不再保留 `bot_id` 字段名或参数别名，所有新/修订链路统一使用 `participant_id`。

## 当前失败链路

当前 PicoClaw runtime env 注入逻辑本质上是：

```text
agentID -> provider.BotConfig(agentID) -> PICOCLAW_CHANNELS_FEISHU_*
```

而 Feishu 配置读取逻辑本质上是：

```text
channels/feishu.toml [bots.<key>] -> provider.BotConfig(<key>)
```

问题在于，已有 worker 重新对接 Feishu 时，`<key>` 可能不是 agent ID。

一个典型失败例子：

```text
已有 agent ID:        u-agent-zcnnq5
新传入 bot_id:        u-gitlab
Feishu participant:   u-gitlab -> u-agent-zcnnq5
Feishu config key:    [bots.u-gitlab]
运行时查询 key:       u-agent-zcnnq5
结果:                 查不到 Feishu 配置，容器没有 Feishu env，不会回复
```

manager 成功的原因不是流程本质不同，而是 ID 恰好重合：

```text
agent ID:             u-manager
Feishu participant:   u-manager -> u-manager
Feishu config key:    [bots.u-manager]
运行时查询 key:       u-manager
结果:                 查得到配置，所以能注入 Feishu env
```

这说明问题不是 Feishu app 本身，也不是 PicoClaw 镜像本身，而是运行时配置查询 key 选错了。

## 目标架构

Feishu 接入的唯一配置主体应该是 Feishu participant。

目标链路：

```text
agentID
  -> 查找绑定到该 agent 的 Feishu participant
  -> 使用 Feishu participant ID 查询 Feishu 配置
  -> 注入 PICOCLAW_CHANNELS_FEISHU_* 到 agent runtime
```

也就是：

```text
agentID -> feishu participant ID -> provider.BotConfig(participantID)
```

不再使用：

```text
agentID -> provider.BotConfig(agentID)
```

也不在运行时保留 `provider.BotConfig(agentID)` fallback。fallback 只会继续掩盖 ID 语义混乱。

## 修改范围草案

### 1. Feishu skill 接入流程

Feishu skill 不应该再把 `bot_id` 当作核心输入。

建议调整为：

- 新参数语义使用 `participant_id`。
- `--participant-id` 为唯一入参，`--bot-id` 本轮不再接受；写入 state 与 provider 时统一使用 `participant_id`。
- 创建 Feishu participant 时，participant ID 使用 `participant_id`。
- 写入 Feishu 配置时，配置 key 使用同一个 `participant_id`。
- 绑定已有 worker 时，明确区分 `participant_id` 和 `agent_id`。

历史 `bots.json`（legacy bot state）里常见的是这类记录（`role` 仅为旧 schema）：

```json
{
  "id": "u-gitlab",
  "channel": "feishu",
  "type": "agent",
  "role": "worker",
  "agent_id": "u-agent-zcnnq5"
}
```

迁移后的 participant state 为：

```json
{
  "id": "gitlab",
  "channel": "feishu",
  "type": "agent",
  "agent_id": "u-agent-zcnnq5"
}
```

`role` 在新 participant state 中不再作为必填字段，若要表达“worker/manager/owner”等角色请看 agent/profile 层。

如果历史 state 仍有 `bot_id` 字段，迁移入口应统一扫描并一次性改写为 `participant_id`；改写后新链路不得再读取或传播 `bot_id` 语义。

### 2. Runtime Feishu env 注入

runtime wiring 需要从 participant 关系反查 Feishu participant。

建议逻辑：

```text
输入: agentID
查询: channel=feishu, type=agent, agent_id=agentID 的 participant
如果找到: 使用 participant.ID 查询 Feishu 配置并注入 env
如果找不到: 不注入 Feishu env
```

这里不做 agentID fallback。

如果未来出现多个 Feishu participant 绑定同一个 agent，再单独设计选择规则。当前先不引入 `runtime_default` 或类似默认值，避免把这次修复扩大成多实例路由设计。

### 3. 旧 feishu.toml 配置迁移

旧服务里 `channels/feishu.toml` 的 key 可能仍是历史 `bot_id`。

兼容逻辑应放在配置迁移阶段，而不是 runtime 查询阶段。

建议迁移逻辑：

```text
读取 participant 数据
读取 channels/feishu.toml
对每个 Feishu participant:
  如果 participant.ID 已有配置:
    保持不动
  如果旧 key 有配置，并且能确认旧 key 对应这个 participant:
    把配置移动到 participant.ID
保存 feishu.toml
刷新 Feishu provider
```

迁移后的文件应变成：

```toml
[bots.<feishu-participant-id>]
app_id = "cli_xxx"
app_secret = "..."
```

虽然 toml section 名仍叫 `[bots]`，但它的 key 语义应被视为 participant ID。是否重命名 toml 结构可以留到后续版本，不建议这次一起做，因为会扩大兼容面。

### 4. 文档和命令说明

文档和 skill prompt 需要统一措辞：

- 不再把 Feishu 本地配置 key 叫 `bot_id`。
- 对用户解释为 Feishu participant ID。
- Feishu 官方 app ID 仍叫 `app_id`。
- 绑定已有 agent 时，要明确传的是已有 `agent_id`，不是 participant ID。

## 已有 agent 是否需要重启或 recreate

对于上一个版本已经真正配置成功 Feishu 的 agent，迁移 `channels/feishu.toml` 的配置 key 后，不应该要求用户立刻重启或 recreate agent runtime。

原因是 agent 容器不会直接读取 CSGClaw 主进程的 `channels/feishu.toml`。它只读取创建 runtime 时已经注入进去的环境变量，例如：

```text
PICOCLAW_CHANNELS_FEISHU_ENABLED
PICOCLAW_CHANNELS_FEISHU_APP_ID
PICOCLAW_CHANNELS_FEISHU_APP_SECRET
```

因此：

- 已经成功配置 Feishu 的旧 agent，容器里已经有 Feishu env，可以继续运行。
- CSGClaw server 重启会重新加载和迁移 `channels/feishu.toml`，但不会移除已经运行中容器里的 Feishu env。
- 修改 `channels/feishu.toml` 的 key 主要影响后续 runtime create/recreate 时如何重新注入 env。
- 后续如果这个 agent 因为升级、重建、手动 recreate 等原因重新创建 runtime，新 runtime wiring 必须能用 Feishu participant ID 找到配置并重新注入 env。
- 原容器里本来就没有 Feishu env 的 agent，说明它之前就没有真正完成 Feishu runtime 配置；这种不属于“已成功配置的旧 agent 不受影响”的范围。它需要在修复后重新 create/recreate，才能拿到 Feishu env。

普通 `docker restart` 也不会改变容器 env；如果一个容器创建时没有 Feishu env，restart 后仍然没有。这个场景应视为之前配置未真正生效，而不是配置迁移造成的影响。

## 改变配置 key 是否影响 agent 读取逻辑

不会直接影响 agent 内部读取逻辑。

agent runtime 内部读取的是环境变量，不读取 toml key。toml key 只影响 CSGClaw 主进程在创建或重建 runtime 时能否找到 Feishu app 凭据。

真正需要保证的是：

```text
Feishu participant ID == channels/feishu.toml 里的配置 key
```

只要 runtime wiring 用 participant ID 查询配置，agent 容器拿到的 env 格式不需要改变。

## 不做的事情

这次不建议做这些事：

- 不在 runtime 里保留 `agentID -> BotConfig(agentID)` fallback。
- 不引入多个 Feishu participant 的默认选择规则。
- 不把 toml section 从 `[bots]` 立刻改名为 `[participants]`。
- 不改变 PicoClaw agent 内部读取 Feishu env 的方式。
- 不把 Feishu 官方 `app_id` 和 CSGClaw 本地 participant ID 混为一谈。

## 待讨论点

### participant ID 是否强制去掉 `u-`

本轮建议改为**强制去掉 `u-` 前缀**并对历史数据做迁移。

架构上更干净的方向是：

```text
agent ID:       u-agent-zcnnq5
participant ID: agent-zcnnq5 或 gitlab
```

历史 Feishu participant ID 里可能存在 `u-manager`、`u-gitlab` 这类值，不能只修改新建路径，必须安全更新历史数据。

安全迁移要求：

1. 在启动迁移中，先扫描 `participant` 持久化 state，找出 Feishu participant 与 Agent 绑定关系。
2. 对 `id` 以 `u-` 开头的 Feishu participant ID 做归一化（如 `u-gitlab -> gitlab`），并在一次事务内完成以下更新：
   - `participant` store 的 participant ID（包括 store key）。
   - 所有引用该 participant 的 room / message / binding / feishu 配置 key。
3. 当目标 `participant_id` 已存在时，采用合并策略（优先保留最近更新项）并记录不可逆冲突，避免静默丢数据。
4. 迁移完成后立即落盘，并在下一步调用 `feishuSvc.ReloadConfig()`。

决议：

- 新建流程统一生成无 `u-` participant ID。
- 历史数据必须全量迁移 `u-` 前缀。
- 如碰到冲突或无法自动合并的数据，迁移阶段返回失败并中止启动，要求人工修复。

### `--bot-id` 是否保留

外部命令不再保留 `--bot-id` 兼容别名，本轮文档与交互统一使用 `--participant-id`。

内部 state、API payload、日志和变量名应尽量改为 `participant_id`，避免继续制造新歧义。

### 旧配置迁移触发时机

迁移需要同时看到 participant 数据和 Feishu config 数据，所以不适合只放在 Feishu file store 的裸 `Load()` 里。

更合适的位置是服务启动后、participant service 和 Feishu service 都初始化完成之后执行一次迁移，然后保存 toml 并刷新 provider。

## 结论

这次问题的真正原因是 Feishu 配置 key 仍沿用旧 `bot_id` 语义，而 runtime 在 participant 架构下仍用 agent ID 查 Feishu 配置。

最合适的修复方向是：

```text
Feishu 接入: participant ID 是唯一配置 key
Runtime 注入: agentID -> Feishu participant ID -> Feishu config
旧数据兼容: 启动迁移 feishu.toml key，而不是 runtime fallback
```

这样可以把语义收敛到 participant 架构上，也能解释 manager、新建 worker、已有 worker 三种表现为什么不同。

## 本轮评审约束确认（非兼容实现版）

1. 本轮不保留 `bot_id` 作为对外语义与兼容入口；所有新/修订链路统一使用 `participant_id`。
2. 运行时注入禁止 `agentID -> BotConfig(agentID)` 回退。
3. 启动迁移必须做到：读取 participant 与 `channels/feishu.toml` 一致性映射后，落盘持久化并刷新 provider。

## 最小改动优先级清单（按文件）

### P0（必须先做）

1. `cli/serve/serve.go`
   - 在 `feishuSvc` 与 `participantSvc` 都可用后，执行一次启动迁移。
   - 做完映射后持久化 `channels/feishu.toml`，并调用 `feishuSvc.ReloadConfig()`。

2. `internal/app/runtimewiring/picoclaw.go`
   - 在 `picoClawRuntimeEnvVars` 里不再把入参 `agentID` 直接当作 Feishu 配置 key。
   - 改为按 `agentID` 查找绑定的 Feishu participant，再以 participantID 调用 `provider.BotConfig(...)`。

3. `internal/app/runtimewiring/openclaw.go`
   - `openClawBoxEnvVars` 里确保 Feishu 相关 account 由 participant 维度决议，不使用 agentID 回退。

4. `internal/runtime/openclawsandbox/config.go`
   - `renderConfig` / `updateOpenClawFeishuChannel` 链路改为按 participantID 查询 Feishu 配置。

### P1（接口与数据模型统一）

5. `internal/apitypes/feishu_config.go`
   - `FeishuConfigRequest/Response` 字段语义改为 `participant_id`。

6. `internal/api/feishu_config.go`
   - GET/PUT 参数与内部展示去 `bot_id`，改为 `participant_id`。

7. `cli/participant/config.go`
   - CLI 配置命令参数改为 `--participant-id`，去除 `--bot-id`。

### P2（测试和文案）

8. `internal/api/feishu_config_test.go`
   - 更新请求/响应字段断言与落盘 Key 断言。
9. `cli/app_test.go`
   - 更新 CLI 调用链路 URL/JSON 字段为 `participant_id`。
10. `internal/runtime/openclawsandbox/config_test.go`
   - 增加 participant 映射导致的 OpenClaw 配置注入测试。
11. 方案文档/用户可见文档
   - 清理 `bot_id` 作为业务主语义的措辞，新增“无兼容重构对齐说明”。
