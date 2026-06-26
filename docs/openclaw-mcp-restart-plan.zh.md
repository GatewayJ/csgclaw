# 通用 Runtime MCP 配置接入方案

本文档约束 CSGClaw 为 agent 增加 MCP 能力的实现方案。文件名保留 OpenClaw 历史语境，但方案本身按通用 runtime 能力设计：MCP 配置先存入 agent 的 `runtime_options.mcp`，再由各 runtime adapter 在 provision/recreate 阶段渲染为对应运行时的原生配置。

阶段一采用“变更后需要重建”的策略：MCP 配置保存后进入 CSGClaw 持久化状态，并标记 agent 需要重建；执行 agent recreate 后，新 runtime 进程加载最新 MCP 配置。暂不处理运行中配置写入触发的 hot reload race，只在关键路径打日志，方便后续分析。

## 1. 目标与边界

目标：

- 所有 runtime kind 使用统一 API 形态：`runtime_options.mcp`。
- MCP 配置由 CSGClaw 管理，随 agent create/update 持久化。
- 创建 agent 时，MCP 配置随首次 provision 生效。
- 修改已有 agent 的 MCP 配置后，标记“需要重建”，通过现有 agent recreate 流程生效。
- runtime adapter 负责把 `runtime_options.mcp` 转成原生配置；OpenClaw adapter 渲染到 `mcp.servers`。

非目标：

- 阶段一不做 MCP marketplace、自动安装、连接测试或动态发现。
- 阶段一不依赖任一 runtime 的 MCP hot reload 保证。
- 阶段一不新增 CLI MCP 参数或 `agent update` 子命令。
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

CSGClaw API 接收与 Multica 一致的 MCP server 输入形态，但位置放在 `runtime_options.mcp`：

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

通用约束：

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

建议增加共享 helper：

```go
const RuntimeOptionMCPKey = "mcp"

func ExtractMCPConfig(runtimeOptions map[string]any) (MCPConfigState, error)
func RuntimeOptionsWithMCP(runtimeOptions map[string]any, mcp any) map[string]any
```

`MCPConfigState` 至少区分：

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
  -> runtime MCP adapter
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

### 4.3 Runtime adapter 策略

每个 runtime kind 都通过同一个 `runtime_options.mcp` 输入获得 MCP 配置，但落地方式由 runtime adapter 决定。

建议定义 runtime MCP policy：

```go
type RuntimeMCPPolicy interface {
    ValidateMCPConfig(runtimeOptions map[string]any) error
    MCPRestartRequired(previous, current map[string]any) bool
}
```

对 provisioner runtime，adapter 在 `Provision` 期间渲染原生配置。对非 provisioner runtime，如果 MCP 需要写入启动前配置，也应把写入逻辑放在 runtime 自己的 host-side preparation 路径中，不要散落到 API handler 或 agent service。

阶段一默认策略：

- `runtime_options.mcp` 变更后需要重建。
- 不依赖 runtime hot reload。
- 如果某个 runtime adapter 已确认支持安全热应用，可以后续单独改 policy。

OpenClaw adapter：

- 渲染到 `openclaw.json` 的 `mcp.servers`。
- `null` 清除 CSGClaw 管理的 `mcp.servers`。
- `{}` 或空 `mcpServers` 写入 `mcp.servers: {}`。

PicoClaw/Codex/其它 runtime：

- 复用相同 `runtime_options.mcp` 输入和校验。
- 由对应 runtime adapter 渲染为其原生 MCP 配置或启动参数。
- 不允许静默忽略已保存 MCP 配置；如果 adapter 暂无可生效路径，应在 validate/provision 阶段返回明确错误，避免用户误以为配置已经生效。

### 4.4 保存与重建

保存已有 agent 的 MCP 配置时：

1. 合并并校验新的 `runtime_options`。
2. 校验 `runtime_options.mcp` 的 JSON 和 server entry。
3. 持久化到 agent state。
4. 如果 `runtime_options.mcp` 发生变化，设置 `agent_profile.env_restart_required = true`。
5. 不主动 stop running runtime。
6. 不依赖 runtime hot reload。

生效方式：

1. 用户点击“重建”或“保存并重建”。
2. 后端调用现有 `POST /api/v1/agents/{id}/recreate`。
3. `Service.Recreate` 删除旧 runtime handle。
4. `Provision` 使用最新 `Agent.RuntimeOptions` 重新生成 runtime 原生配置。
5. 新 runtime 进程启动并加载 MCP。
6. `persistRecreatedAgent` 成功后清除 `env_restart_required`。

这不是重启 CSGClaw server，而是重建该 agent 的 runtime 进程。

### 4.5 与现有 profile 热同步路径的关系

当前 gateway runtime 的 profile runtime input 变化时，CSGClaw 已有 `syncGatewayHostConfig` 路径会重写 host config。阶段一暂不处理这条路径和 MCP 变更之间的 hot reload race。

实现要求：

- 保持现有 `syncGatewayHostConfig` 行为。
- 不因为 MCP pending 状态阻塞 profile 配置热同步。
- 在 `syncGatewayHostConfig` 即将写 host config 时，如果当前 agent 配置了 `runtime_options.mcp` 或 `env_restart_required = true`，打印结构化日志。
- 日志至少包含 `agent_id`、`agent_name`、`runtime_kind`、`mcp_configured`、`env_restart_required`、`stage`。
- 日志不要求解决 race，只要求后续能定位“保存 MCP 后是否又发生了 host config 写入”。

建议日志点：

- MCP 配置保存成功。
- MCP 配置清除成功。
- runtime provision 开始渲染 MCP。
- runtime provision 写入原生配置成功。
- `syncGatewayHostConfig` 在 MCP configured 或 restart required 状态下执行。

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

成功返回沿用现有 agent response，`runtime_options` 中包含当前 MCP 配置：

```json
{
  "agent_profile": {
    "env_restart_required": true
  },
  "runtime_options": {
    "mcp": {
      "mcpServers": {}
    }
  }
}
```

前端继续使用已有 `env_restart_required` badge 和 recreate action。

## 6. 前端 UI 设计

### 6.1 页面位置

在现有 Agent 创建和编辑路径中增加通用 MCP 区域：

- 创建 agent modal：`AgentProfileModal` 的 runtime section 下展示。
- Agent 详情页：`AgentDetailPane` 的 runtime section 下展示。
- 控件读写 `draft.runtime_options.mcp`，不引入 top-level `mcp_config` 状态。

MCP 区域可以做成专用组件，但它必须作为 runtime options 的编辑器存在：

```text
MCPRuntimeOptionsPanel
```

### 6.2 区域结构

区域内容：

- 标题：`MCP Servers`
- JSON 编辑器：textarea 或轻量 code editor，保存前做 JSON parse。
- 操作按钮：保存、保存并重建、清除配置、取消。
- 状态提示：有未生效变更时显示“重建后生效”，并提供“重建”按钮。

创建 agent 时：

- 展示空 JSON 编辑器。
- 提供示例填充按钮。
- 保存创建后直接生效，不显示“需要重建”。

编辑已有 agent 时：

- 默认把现有 `runtime_options.mcp` 填回编辑器。
- 用户保存时，前端将编辑后的 MCP 合并回完整 `runtime_options` 后提交。
- 用户选择“清除配置”后，提交合并后的 `runtime_options`，其中 `mcp` 为 `null` 或删除 `mcp` key。
- 保存成功但未重建时，使用现有 `profileRestartRequired` badge，并在详情页 runtime section 展示重建 CTA。

### 6.3 重建交互

MCP 修改保存后，前端不自动刷新或假装生效。

推荐交互：

1. 用户点击“保存”：
   - 保存合并后的 `runtime_options`。
   - 返回 agent response。
   - 显示 `Recreate required` badge。
   - 在 MCP 区域显示“已保存，重建后生效”状态。
2. 用户点击“保存并重建”：
   - 先调用 update。
   - update 成功后调用 `POST /api/v1/agents/{id}/recreate`。
   - recreate 成功后刷新 agent 列表和详情。
3. 用户点击已有 agent action 的“重建”：
   - 复用现有 recreate action。
   - 成功后 `env_restart_required` 清除。

如果 agent 正在运行，保存 MCP 配置不停止当前 runtime。当前 runtime 继续使用旧 MCP 配置，直到 recreate 完成。

## 7. 验证计划

后端测试：

- `runtime_options.mcp` 的缺失、`null`、空对象、空 `mcpServers`、非空 `mcpServers` 语义正确。
- 非 object server entry 报错。
- 缺少 `command` 和 `url` 的 server entry 报错。
- 保存 MCP 时保留其它 runtime options。
- MCP 配置变更后 agent 标记 `env_restart_required`。
- `Recreate` 后重新 provision，并把最新 MCP 传给 runtime adapter。
- `openclawsandbox.renderConfig` 能把 `runtime_options.mcp.mcpServers` 渲染为 `mcp.servers`。
- OpenClaw `null` 清除和空集合渲染行为正确。
- `syncGatewayHostConfig` 在 MCP configured 或 restart required 状态下会打出可诊断日志。

前端测试：

- `AgentProfileModal` 能编辑 `runtime_options.mcp`。
- `AgentDetailPane` 能显示和编辑 `runtime_options.mcp`。
- 保存 MCP 时不覆盖其它 runtime options。
- 替换 MCP 时提交合并后的 `runtime_options`。
- 清除 MCP 时提交合并后的 `runtime_options`。
- 保存并重建按顺序调用 update 和 recreate。

手工验证：

1. 创建 agent，带一个 stdio MCP server。
2. 对 OpenClaw agent，检查宿主机 `~/.csgclaw/agents/<agent>/.openclaw/openclaw.json` 包含 `mcp.servers`。
3. 启动 agent 后确认 runtime 能看到 MCP tool。
4. 修改 MCP 配置并只保存，确认 UI 标记需要重建，旧 runtime 不被停止。
5. 点击重建，确认新配置生效，`env_restart_required` 清除。
6. 在 MCP pending 状态下修改 profile，确认日志能看到 host config sync 记录。

## 8. 实施顺序

1. 增加 `runtime_options.mcp` parser/validator 和共享 helper。
2. 扩展 provision request，把 `Agent.RuntimeOptions` 传到 runtime provision 边界。
3. 增加通用 MCP restart policy：`runtime_options.mcp` 变化后标记 `env_restart_required`。
4. 增加关键路径结构化日志，尤其是 MCP save/provision 和 gateway host config sync。
5. 实现 OpenClaw adapter，把 `runtime_options.mcp.mcpServers` 渲染到 `openclaw.json` 的 `mcp.servers`。
6. 接入其它 runtime adapter 的 MCP 原生渲染路径；没有可生效路径时返回明确错误，不静默忽略。
7. 接入前端 Agent 创建/详情页 MCP runtime options UI。
8. 补充 `docs/api.zh.md` 和相关英文 API 文档。
