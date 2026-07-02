# OpenClaw Runtime MCP 配置接入方案

本文档约束 CSGClaw 为 OpenClaw agent 增加 MCP 能力的阶段一实现方案。阶段一只支持 `openclaw_sandbox`：MCP 配置先存入 agent 的 `runtime_options.mcp`，再由 OpenClaw runtime adapter 在 provision/recreate 阶段渲染为 OpenClaw 原生配置。非 OpenClaw runtime 暂不接收 `runtime_options.mcp`，后续 runtime 如需支持再单独接入。

阶段一沿用现有 gateway runtime 配置变更路径：MCP 配置保存后进入 CSGClaw 持久化状态，并立即触发现有 agent recreate 流程；recreate 成功后，新 runtime 进程加载最新 MCP 配置，`env_restart_required` 被清除。recreate 失败时，已保存的 MCP 配置保留在 agent state 中，`env_restart_required` 继续为 true，用户可通过现有“重建”动作重试。阶段一不依赖 runtime hot reload。

## 1. 目标与边界

目标：

- OpenClaw agent 使用现有 API 形态：`runtime_options.mcp`。
- MCP 配置由 CSGClaw 管理，随 OpenClaw agent create/update 持久化。
- 创建 agent 时，MCP 配置随首次 provision 生效。
- 修改已有 agent 的 MCP 配置后，后端通过现有 agent recreate 流程自动生效。
- OpenClaw adapter 负责把 `runtime_options.mcp` 转成 `openclaw.json` 中的 `mcp.servers`。
- 非 OpenClaw runtime 收到 `runtime_options.mcp` 时返回明确错误，不静默忽略。

非目标：

- 阶段一不做 MCP marketplace、自动安装、连接测试或动态发现。
- 阶段一不依赖任一 runtime 的 MCP hot reload 保证。
- 阶段一不新增 CLI MCP 参数或 `agent update` 子命令。
- 阶段一不抽象通用 MCP runtime policy，也不接入 PicoClaw/Codex。
- 暂不处理 MCP secret 脱敏体验；API 和前端沿用 `runtime_options` 的现有回显语义。

## 2. 当前 OpenClaw 配置注入路径

OpenClaw 是第一条需要落地的 runtime adapter。CSGClaw 当前已经通过配置文件注入 OpenClaw 运行参数，路径如下：

```text
Agent create/recreate provisioning
  -> internal/agent.Service.provisionRuntime(...)
  -> openclawsandbox.Runtime.Provision(...)
  -> openclawsandbox.EnsureConfig(...)
  -> renderConfig(defaults/openclaw-gateway.json + CSGClaw runtime inputs)
  -> ~/.csgclaw/agents/<agent-name>/.openclaw/openclaw.json
```

`EnsureConfig` 会创建宿主机目录：

```text
~/.csgclaw/agents/<agent-name>/.openclaw/
```

并写入：

```text
~/.csgclaw/agents/<agent-name>/.openclaw/openclaw.json
~/.csgclaw/agents/<agent-name>/.openclaw/exec-approvals.json
```

OpenClaw sandbox 创建时，CSGClaw 会把该目录挂载进容器：

```text
宿主机: ~/.csgclaw/agents/<agent-name>/.openclaw
容器内: /home/node/.openclaw
```

同时设置：

```text
HOME=/home/node
```

OpenClaw gateway 启动命令为：

```bash
node /app/openclaw.mjs gateway --allow-unconfigured --bind lan --port 18789
```

因此 OpenClaw 读取的是默认路径：

```text
/home/node/.openclaw/openclaw.json
```

结论：OpenClaw MCP adapter 不需要写 workspace 文件，也不需要通过 task wrapper 或 `OPENCLAW_CONFIG_PATH` 指向临时文件；它应该继续维护每个 agent 的长期 OpenClaw 配置根目录，并在 provision/recreate 时把 `runtime_options.mcp` 渲染进 `openclaw.json`。

## 3. MCP 配置形态

CSGClaw API 接收与 Multica 一致的 MCP server 配置输入形态，但位置放在 `runtime_options.mcp`。这里的 `mcpServers` 是配置包裹形态，不是 MCP 标准协议的 JSON-RPC 消息格式；真正的 MCP 协议交互由 OpenClaw runtime 和对应 MCP server 处理。

```json
{
  "runtime_kind": "openclaw_sandbox",
  "runtime_options": {
    "mcp": {
      "mcpServers": {
        "context7": {
          "command": "uvx",
          "args": ["context7-mcp"],
          "env": {
            "CONTEXT7_API_KEY": "secret"
          }
        },
        "remote-search": {
          "url": "https://mcp.example.com/mcp",
          "transport": "streamable-http",
          "headers": {
            "Authorization": "Bearer secret"
          }
        }
      }
    }
  }
}
```

OpenClaw adapter 渲染到 OpenClaw 原生配置：

```json
{
  "mcp": {
    "servers": {
      "context7": {
        "command": "uvx",
        "args": ["context7-mcp"],
        "env": {
          "CONTEXT7_API_KEY": "secret"
        }
      }
    }
  }
}
```

输入约束：

- 持久化后的 `runtime_options.mcp` 缺失表示该 agent 没有 CSGClaw 管理的 MCP 配置。
- API update 的字段缺失语义以第 5.2 节为准：`runtime_options` 整体缺失才表示不修改；`runtime_options` 一旦提交就是整体替换。
- `runtime_options.mcp: null` 表示清除 CSGClaw 管理的 MCP 配置。
- `runtime_options.mcp: {}` 或 `{"mcpServers": {}}` 表示显式托管空集合。
- 非空 `mcpServers` 表示使用 CSGClaw 托管集合。
- 每个 server entry 必须是 JSON object。
- 每个 server entry 必须声明 `command` 或 `url`。
- stdio server 使用 `command`、`args`、`env`。
- HTTP/SSE server 使用 `url`，并直接使用目标 runtime 识别的 `transport`。
- server 名称排序后写入，保证生成配置稳定。

OpenClaw 约束：

- `runtime_options.mcp.mcpServers` 渲染为 OpenClaw `mcp.servers`。
- 不支持直接提交完整 OpenClaw `mcp` 对象，避免用户把 OpenClaw 其它 `mcp.*` 设置和 server 列表混在一起造成合并语义不清。
- MCP command 在 OpenClaw sandbox 内执行，不在宿主机执行；需要的二进制必须在镜像内存在，或通过 workspace/projects 等已有挂载路径可访问。

## 4. 后端设计

### 4.1 数据模型

不要新增 top-level `Agent.MCPConfig`。阶段一把 MCP 配置放在 `Agent.RuntimeOptions["mcp"]`，保持和已有 runtime option 架构一致。

需要同步处理：

- `agent.CreateAgentSpec.RuntimeOptions`
- `agent.UpdateRequest.RuntimeOptions`
- `apitypes.CreateAgentRequest.RuntimeOptions`
- participant agent binding 中的 create payload
- Web `AgentLike.runtime_options`
- runtime option 保存/合并逻辑

注意：当前 `PATCH /api/v1/agents/{id}` 的 `runtime_options` 是整体替换语义。前端保存 MCP 时必须基于当前 agent 的 `runtime_options` 合并后提交，避免只提交 `{"mcp": ...}` 时覆盖其它 runtime option，例如 Codex 的 `local_workspace_dir`。

实现时保留现有 runtime option 边界，只增加最小共享常量和 OpenClaw 内部解析逻辑：

```go
const RuntimeOptionMCPKey = "mcp"
```

OpenClaw 解析状态至少区分：

- absent
- cleared/null
- empty managed set
- managed server set

### 4.2 ProvisionRequest 传递

因为 MCP 已放入 `runtime_options`，provision 边界应传递通用 runtime options，而不是新增 MCP 专用字段：

```go
type ProvisionRequest struct {
    RuntimeOptions map[string]any
}
```

调用链：

```text
Agent.RuntimeOptions["mcp"]
  -> Service.provisionRuntimeForAgent(...)
  -> agentruntime.ProvisionRequest.RuntimeOptions
  -> concrete runtime Provision(...)
  -> OpenClaw MCP adapter
  -> native runtime config
```

OpenClaw adapter 的调用链：

```text
runtime_options.mcp
  -> openclawsandbox.Runtime.Provision(...)
  -> openclawsandbox.EnsureConfig(...)
  -> renderConfig(...)
  -> updateOpenClawMCP(...)
  -> ~/.csgclaw/agents/<agent-name>/.openclaw/openclaw.json
```

`openclawsandbox.EnsureConfig` 新增 runtime options 参数后，`renderConfig` 在现有步骤之后追加：

```text
updateOpenClawModelProvider
updateOpenClawCsgclawChannel
updateOpenClawFeishuChannel
updateOpenClawGatewayAuth
updateOpenClawMCP
```

### 4.3 OpenClaw adapter 策略

阶段一复用现有 `RuntimeConfigController` 扩展点，不新增 MCP 专用跨 runtime 抽象。OpenClaw runtime controller 负责：

- 校验 `runtime_options.mcp`。
- 判断 `runtime_options.mcp` 变更是否需要 recreate。
- 在 `Provision` 期间把 MCP 配置渲染进 `openclaw.json`。

OpenClaw adapter：

- 渲染到 `openclaw.json` 的 `mcp.servers`。
- `null` 清除 CSGClaw 管理的 `mcp.servers`。
- `{}` 或空 `mcpServers` 写入 `mcp.servers: {}`。

非 OpenClaw runtime：

- 阶段一不支持 `runtime_options.mcp`。
- create/update/start 等需要校验 runtime config 的路径遇到 `runtime_options.mcp` 时返回明确错误。

### 4.4 保存与自动重建

保存已有 agent 的 MCP 配置时：

1. 合并并校验新的 `runtime_options`。
2. 校验 `runtime_options.mcp` 的 JSON 和 server entry。
3. 持久化到 agent state。
4. 如果 `runtime_options.mcp` 发生变化，设置 `agent_profile.env_restart_required = true`，作为持久化重建标记。
5. 对支持 MCP 渲染的 gateway runtime，立即调用现有 `Service.Recreate`。
6. `Recreate` 成功后，新 runtime 进程加载最新 MCP 配置，`persistRecreatedAgent` 清除 `env_restart_required`。
7. `Recreate` 失败时，返回错误；已保存 MCP 配置保留，`env_restart_required` 保留为 true，用户可通过现有 recreate action 重试。
8. 不依赖 runtime hot reload。

生效方式：

1. 用户保存 MCP 配置。
2. 后端持久化最新 `Agent.RuntimeOptions`。
3. 后端调用现有 `Service.Recreate`。
4. `Service.Recreate` 删除旧 runtime handle。
5. `Provision` 使用最新 `Agent.RuntimeOptions` 重新生成 runtime 原生配置。
6. 新 runtime 进程启动并加载 MCP。
7. `persistRecreatedAgent` 成功后清除 `env_restart_required`。

这不是重启 CSGClaw server，而是重建该 agent 的 runtime 进程。

### 4.5 与现有 profile 热同步路径的关系

当前 gateway runtime 的 profile runtime input 变化时，CSGClaw 已有 `syncGatewayHostConfig` 路径会重写 host config。因为 OpenClaw 配置是从 embedded defaults 重新渲染，而不是读旧文件做增量修改，所以该路径也必须接收当前 agent 的 `runtime_options`，并在重写 host config 时保留/渲染 `runtime_options.mcp`。否则 profile 热同步会把已经生效的 `mcp.servers` 覆盖掉。

实现要求：

- 保持现有 `syncGatewayHostConfig` 的 profile 热同步能力。
- `syncGatewayHostConfig` 调用 OpenClaw `EnsureConfig` 时必须传入当前 `Agent.RuntimeOptions`。
- `EnsureConfig`/`renderConfig` 每次写 host config 都应根据当前 `runtime_options.mcp` 渲染 runtime 原生 MCP 配置。

## 5. API 设计

### 5.1 Create Agent

`POST /api/v1/agents` 和 participant agent binding create payload 使用现有 `runtime_options`：

```json
{
  "runtime_kind": "openclaw_sandbox",
  "runtime_options": {
    "mcp": {
      "mcpServers": {
        "context7": {
          "command": "uvx",
          "args": ["context7-mcp"]
        }
      }
    }
  }
}
```

创建时 MCP 配置直接参与 provision，agent 首次启动后生效，不需要额外 recreate。

### 5.2 Update Agent

`PATCH /api/v1/agents/{id}` 继续使用 `runtime_options`：

```json
{
  "runtime_options": {
    "mcp": {
      "mcpServers": {
        "context7": {
          "command": "uvx",
          "args": ["context7-mcp"]
        }
      }
    }
  }
}
```

语义：

- `runtime_options` 字段缺失：不修改 runtime options，也不修改 MCP。
- `runtime_options` 字段存在：整体替换当前 runtime options。
- `runtime_options.mcp` 缺失：提交后的 runtime options 不再包含 MCP。
- `runtime_options.mcp: null`：清除 MCP。
- `runtime_options.mcp` 为 object：替换整个 MCP 配置。

因此前端保存 MCP 时应总是提交合并后的完整 `runtime_options`：

```json
{
  "runtime_options": {
    "local_workspace_dir": "/path/kept-from-existing-options",
    "mcp": {
      "mcpServers": {
        "context7": {
          "command": "uvx",
          "args": ["context7-mcp"]
        }
      }
    }
  }
}
```

成功返回沿用现有 agent response，`runtime_options` 中包含当前 MCP 配置。已有 agent 的 MCP 变更会在返回前尝试自动 recreate；如果 recreate 成功，`env_restart_required` 为 false 或省略：

```json
{
  "agent_profile": {
    "env_restart_required": false
  },
  "runtime_options": {
    "mcp": {
      "mcpServers": {}
    }
  }
}
```

如果 update 已持久化但 recreate 失败，后端返回错误；前端应刷新 agent 状态并继续使用已有 `env_restart_required` badge 和 recreate action 作为重试入口。

## 6. 前端 UI 设计

### 6.1 页面位置

在现有 Agent 创建和编辑路径中增加 OpenClaw MCP 区域：

- 创建 agent modal：`AgentProfileModal` 的 runtime section 下展示。
- Agent 详情页：`AgentDetailPane` 的 runtime section 下展示。
- 仅当 draft 的 `runtime_kind` 为 `openclaw_sandbox` 时展示。
- 控件读写 `draft.runtime_options.mcp`，不引入 top-level `mcp_config` 状态。

MCP 区域可以做成专用组件，但它必须作为 runtime options 的编辑器存在：

```text
MCPRuntimeOptionsPanel
```

### 6.2 区域结构

区域内容：

- 标题：`MCP Servers`
- JSON 编辑器：textarea 或轻量 code editor，保存前做 JSON parse。
- 操作按钮：填入示例、清除配置。
- 保存动作复用创建/编辑表单已有保存按钮，不在 MCP 面板内新增独立保存流程。

创建 agent 时：

- 展示空 JSON 编辑器。
- 提供示例填充按钮。
- 保存创建后直接生效，不显示“需要重建”。

编辑已有 agent 时：

- 默认把现有 `runtime_options.mcp` 填回编辑器。
- 用户保存时，前端将编辑后的 MCP 合并回完整 `runtime_options` 后提交。
- 用户选择“清除配置”后，提交合并后的 `runtime_options`，其中 `mcp` 为 `null` 或删除 `mcp` key。
- 保存成功表示后端已完成自动 recreate 并刷新为新 runtime。
- 保存失败时刷新 agent；如果 `env_restart_required` 为 true，使用现有 `profileRestartRequired` badge，并在详情页 runtime section 展示重建 CTA。

### 6.3 自动重建交互

MCP 修改保存后，后端负责自动重建 runtime；前端不再提供专门的“保存并重建”主流程。

推荐交互：

1. 用户点击“保存”：
   - 保存合并后的 `runtime_options`。
   - 后端自动调用 recreate。
   - update 成功返回 agent response。
   - 刷新 agent 列表和详情。
   - 不显示 `Recreate required` badge。
2. update 返回错误：
   - 展示错误。
   - 刷新 agent 列表和详情。
   - 如果刷新后的 agent 仍有 `env_restart_required`，显示现有重建 CTA。
3. 用户点击已有 agent action 的“重建”：
   - 复用现有 recreate action。
   - 成功后 `env_restart_required` 清除。

如果 agent 正在运行，保存 MCP 配置会触发 runtime recreate。当前路径会重建该 agent 的 runtime 进程，不依赖 hot reload。

## 7. 验证计划

后端测试：

- `runtime_options.mcp` 的缺失、`null`、空对象、空 `mcpServers`、非空 `mcpServers` 语义正确。
- 非 object server entry 报错。
- 缺少 `command` 和 `url` 的 server entry 报错。
- 保存 MCP 时保留其它 runtime options。
- MCP 配置变更后先标记 `env_restart_required`，随后自动调用 `Recreate`。
- `Recreate` 后重新 provision，并把最新 MCP 传给 runtime adapter。
- 自动 `Recreate` 成功后清除 `env_restart_required`。
- 自动 `Recreate` 失败后保留已保存的 `runtime_options.mcp` 和 `env_restart_required = true`。
- `openclawsandbox.renderConfig` 能把 `runtime_options.mcp.mcpServers` 渲染为 `mcp.servers`。
- OpenClaw `null` 清除和空集合渲染行为正确。
- 非 OpenClaw runtime 收到 `runtime_options.mcp` 时返回明确错误。
- `syncGatewayHostConfig` 重写 OpenClaw host config 时不会丢失 MCP 配置。

前端测试：

- `AgentProfileModal` 能编辑 `runtime_options.mcp`。
- `AgentDetailPane` 能显示和编辑 `runtime_options.mcp`。
- 保存 MCP 时不覆盖其它 runtime options。
- 替换 MCP 时提交合并后的 `runtime_options`。
- 清除 MCP 时提交合并后的 `runtime_options`。
- 保存已有 agent 的 MCP 配置时只调用 update；recreate 由后端自动完成。
- update 失败后刷新 agent，并能展示现有 recreate required 状态。

手工验证：

1. 创建 agent，带一个 stdio MCP server。
2. 对 OpenClaw agent，检查宿主机 `~/.csgclaw/agents/<agent>/.openclaw/openclaw.json` 包含 `mcp.servers`。
3. 启动 agent 后确认 runtime 能看到 MCP tool。
4. 修改 MCP 配置并保存，确认后端自动重建 runtime。
5. 自动重建成功后确认新配置生效，`env_restart_required` 清除。
6. 模拟 recreate 失败，确认已保存 MCP 配置保留，`env_restart_required` 为 true，现有重建 action 可恢复。
7. MCP 配置生效后修改 profile，确认 host config sync 不会丢失 `mcp.servers`，并能看到诊断日志。

## 8. 实施顺序

1. 增加 `runtime_options.mcp` parser/validator 和共享 helper。
2. 扩展 provision request，把 `Agent.RuntimeOptions` 传到 runtime provision 边界。
3. 增加 OpenClaw runtime config controller：`runtime_options.mcp` 变化后标记 `env_restart_required`，并自动调用现有 `Recreate`。
4. 实现 OpenClaw adapter，把 `runtime_options.mcp.mcpServers` 渲染到 `openclaw.json` 的 `mcp.servers`。
5. 让 `syncGatewayHostConfig` 重写 OpenClaw host config 时也传入并渲染当前 `Agent.RuntimeOptions`，避免 profile 热同步丢失 MCP。
6. 拒绝非 OpenClaw runtime 的 `runtime_options.mcp`。
7. 接入前端 Agent 创建/详情页 MCP runtime options UI。
8. 补充 `docs/api.zh.md` 和相关英文 API 文档。
