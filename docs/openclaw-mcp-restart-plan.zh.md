# OpenClaw MCP 配置接入方案

本文档约束 CSGClaw 为 `openclaw_sandbox` worker 增加 MCP 能力的实现方案。阶段一采用“变更后需要重建”的策略：MCP 配置保存后先进入 CSGClaw 持久化状态，只有执行 agent recreate 后才写入并生效到新的 OpenClaw gateway 进程。

## 1. 目标与边界

目标：

- 只支持 `openclaw_sandbox` worker 的 MCP 配置。
- MCP 配置由 CSGClaw 管理，并渲染到 OpenClaw 原生 `mcp.servers` 配置块。
- 创建 agent 时 MCP 配置随 OpenClaw 初次启动直接生效。
- 修改已有 agent 的 MCP 配置后，标记“需要重建”，通过现有 agent recreate 流程生效。
- API 和前端不回显 MCP secret 明文。

非目标：

- 不在阶段一支持 PicoClaw MCP。
- 不改 bootstrap manager 的运行时策略。
- 不依赖 OpenClaw gateway 热加载 MCP。
- 不把 MCP 写入 workspace 文件让 OpenClaw 扫描发现。
- 不做 MCP marketplace、自动安装、连接测试或动态发现。

## 2. 当前 OpenClaw 配置注入路径

CSGClaw 当前已经通过配置文件注入 OpenClaw 运行参数，路径如下：

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

结论：CSGClaw 的 OpenClaw 配置注入不是把文件写进 workspace，也不是通过 task wrapper 或 `OPENCLAW_CONFIG_PATH` 指向临时文件，而是维护每个 agent 的长期 OpenClaw 配置根目录，并通过 sandbox mount 让 OpenClaw gateway 按默认路径读取。

## 3. MCP 配置形态

CSGClaw API 建议接收与 Multica 一致的输入形态：

```json
{
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
```

渲染到 OpenClaw 时转换为 OpenClaw 原生配置：

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

约束：

- 每个 server entry 必须是 JSON object。
- 每个 server entry 必须声明 `command` 或 `url`。
- stdio server 使用 `command`、`args`、`env`。
- HTTP/SSE server 使用 `url`，并直接使用 OpenClaw 识别的 `transport`，例如 `streamable-http`，不自动把 Claude 的 `type` 翻译成 OpenClaw 字段。
- server 名称排序后写入，保证 `openclaw.json` 输出稳定。
- MCP command 在 OpenClaw sandbox 内执行，不在宿主机执行；需要的二进制必须在镜像内存在，或通过 workspace/projects 等已有挂载路径可访问。

三态语义：

| 输入 | 语义 | 渲染结果 |
| --- | --- | --- |
| 字段缺失 | 保持现有 MCP 配置不变 | update 时不修改保存值 |
| `null` | 清除 CSGClaw 管理的 MCP 配置 | recreate 后不写 `mcp.servers` |
| `{}` 或 `{"mcpServers": {}}` | 显式托管空集合 | recreate 后写入 `mcp.servers: {}` |
| 非空 `mcpServers` | 使用 CSGClaw 托管集合 | recreate 后写入对应 `mcp.servers` |

阶段一不支持直接提交完整 OpenClaw `mcp` 对象。这样可以保持 API 稳定，并避免用户把 OpenClaw 其它 `mcp.*` 设置和 server 列表混在一起造成合并语义不清。

## 4. 后端设计

### 4.1 数据模型

不要把 MCP 配置放进 `RuntimeOptions`。当前 `runtime_options` 会原样出现在 API 和前端模型中，MCP 配置可能包含 `env`、`headers`、token 等 secret。

建议新增显式字段：

```go
type Agent struct {
    MCPConfig json.RawMessage `json:"mcp_config,omitempty"`
}
```

需要同步扩展：

- `agent.Agent`
- `agent.persistedAgent`
- `agent.CreateAgentSpec`
- `agent.UpdateRequest`
- `apitypes.CreateAgentRequest`
- API response 的 redacted MCP summary
- participant agent binding 中的 create/update payload

API response 不返回 `mcp_config` 原文，返回脱敏摘要：

```json
{
  "mcp_configured": true,
  "mcp_servers": [
    {
      "name": "context7",
      "kind": "stdio",
      "command": "uvx",
      "env_keys": ["CONTEXT7_API_KEY"]
    },
    {
      "name": "remote-search",
      "kind": "http",
      "url": "https://mcp.example.com/mcp",
      "header_keys": ["Authorization"]
    }
  ]
}
```

### 4.2 ProvisionRequest 传递

在 runtime provision 边界加入 MCP 配置：

```go
type ProvisionRequest struct {
    MCPConfig json.RawMessage
}
```

调用链：

```text
Agent.MCPConfig
  -> Service.provisionRuntimeForAgent(...)
  -> agentruntime.ProvisionRequest.MCPConfig
  -> openclawsandbox.Runtime.Provision(...)
  -> openclawsandbox.EnsureConfig(...)
  -> renderConfig(...)
  -> openclaw.json
```

`openclawsandbox.EnsureConfig` 新增参数后，`renderConfig` 在现有步骤之后追加：

```text
updateOpenClawModelProvider
updateOpenClawCsgclawChannel
updateOpenClawFeishuChannel
updateOpenClawGatewayAuth
updateOpenClawMCP
```

`updateOpenClawMCP` 负责解析 CSGClaw 保存的 `mcp_config`，并写入 `cfg["mcp"]["servers"]`。

### 4.3 保存与重建

MCP 修改时不直接依赖 OpenClaw hot reload。

保存已有 agent 的 MCP 配置时：

1. 校验 JSON 和 server entry。
2. 持久化到 agent state。
3. 如果 runtime kind 是 `openclaw_sandbox`，设置 `agent_profile.env_restart_required = true`。
4. 不主动写运行中的 `/home/node/.openclaw/openclaw.json`。
5. 不主动 stop running gateway。

生效方式：

1. 用户点击“重建”或“保存并重建”。
2. 后端调用现有 `POST /api/v1/agents/{id}/recreate`。
3. `Service.Recreate` 删除旧 sandbox handle。
4. `Provision` 使用最新 `Agent.MCPConfig` 重新写入宿主机 `openclaw.json`。
5. 新 sandbox 挂载同一 `.openclaw` 目录并启动 OpenClaw gateway。
6. `persistRecreatedAgent` 成功后清除 `env_restart_required`。

这不是重启 CSGClaw server，而是重建该 OpenClaw agent 的 sandbox/gateway。

### 4.4 与现有 profile 更新路径的关系

当前 OpenClaw profile runtime input 变化时，CSGClaw 已有 `syncGatewayHostConfig` 路径会重写 OpenClaw host config。MCP 阶段一不要复用这条热更新路径。

原因：

- `EnsureConfig` 当前已有注释说明，运行中写 `openclaw.json` 会触发 OpenClaw hot reload，并可能遇到 gateway lock race。
- MCP server 可能创建子进程、HTTP session、tool registry cache，热替换行为需要 OpenClaw 侧明确保证。
- recreate 能用现有生命周期语义保证新 gateway 从干净状态加载 MCP。

## 5. API 设计

### 5.1 Create Agent

`POST /api/v1/agents` 和 participant agent binding create payload 增加：

```json
{
  "runtime_kind": "openclaw_sandbox",
  "mcp_config": {
    "mcpServers": {
      "context7": {
        "command": "uvx",
        "args": ["context7-mcp"]
      }
    }
  }
}
```

创建时 MCP 配置直接参与 provision，agent 首次启动后生效，不需要额外 recreate。

### 5.2 Update Agent

`PATCH /api/v1/agents/{id}` 增加：

```json
{
  "mcp_config": {
    "mcpServers": {
      "context7": {
        "command": "uvx",
        "args": ["context7-mcp"]
      }
    }
  }
}
```

语义：

- 字段缺失：不修改 MCP。
- `mcp_config: null`：清除 MCP。
- object：替换整个 MCP 配置。

成功返回的 agent response 应带：

```json
{
  "agent_profile": {
    "env_restart_required": true
  },
  "mcp_configured": true,
  "mcp_servers": []
}
```

前端可继续使用已有 `env_restart_required` badge 和 recreate action。

### 5.3 可选 CLI

后续可以给 CLI 增加：

```bash
csgclaw agent create --name alice --runtime openclaw_sandbox --mcp-config-file ./mcp.json
csgclaw agent update alice --mcp-config-file ./mcp.json
csgclaw agent update alice --clear-mcp-config
```

不建议提供 `--mcp-config '<json>'` 作为主入口，因为 secret 容易进入 shell history。若实现 inline 参数，也必须在帮助文本里提示风险。

## 6. 前端 UI 设计

### 6.1 页面位置

在现有 Agent 编辑路径中增加 OpenClaw 专属 MCP 区域：

- 创建 agent modal：`AgentProfileModal` 的 runtime section 下，只有选择 `openclaw_sandbox` 时展示。
- Agent 详情页：`AgentDetailPane` 的 runtime section 下，只有当前 agent 是 `openclaw_sandbox` 时展示。

不要把 MCP 做成通用 `RuntimeOptionsFields`。现有 runtime options 控件只支持简单 input/directory，无法承载 JSON 编辑、secret 脱敏、清除语义和 recreate 提示。

### 6.2 区域结构

建议新增一个页面私有或业务组件：

```text
OpenClawMCPConfigPanel
```

区域内容：

- 标题：`MCP Servers`
- 摘要表：server name、类型、command 或 URL、env/header keys。
- JSON 编辑器：textarea 或轻量 code editor，保存前做 JSON parse。
- 操作按钮：保存、保存并重建、清除配置、取消。
- 状态提示：有未生效变更时显示“重建后生效”，并提供“重建”按钮。

创建 agent 时：

- 展示空 JSON 编辑器。
- 提供示例填充按钮。
- 保存创建后直接生效，不显示“需要重建”。

编辑已有 agent 时：

- 默认展示脱敏摘要。
- JSON 编辑区为空，placeholder 表示留空不修改现有 MCP 配置。
- 用户选择“替换配置”后输入完整 JSON。
- 用户选择“清除配置”后提交 `mcp_config: null`。
- 保存成功但未重建时，使用现有 `profileRestartRequired` badge，并在详情页 runtime section 展示重建 CTA。

### 6.3 Secret 处理

前端不应拿到原始 secret，因此不能默认把现有 MCP JSON 原样填回编辑器。

编辑体验采用“摘要 + 替换”：

- 摘要表只显示 server 名称和非敏感字段。
- `env` 只显示 key，不显示 value。
- `headers` 只显示 key，不显示 value。
- 如果用户要修改已有 server，需要重新提交完整 `mcp_config`。
- 如果用户只想保留现有配置，保存其它 agent 字段时不发送 `mcp_config`。

### 6.4 重建交互

MCP 修改保存后，前端不自动刷新或假装生效。

推荐交互：

1. 用户点击“保存”：
   - 保存配置。
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

如果 agent 正在运行，保存 MCP 配置不停止当前 sandbox。当前 OpenClaw gateway 继续使用旧 MCP 配置，直到 recreate 完成。

## 7. 验证计划

后端测试：

- `openclawsandbox.renderConfig` 能把 `mcp_config.mcpServers` 渲染为 `mcp.servers`。
- `null`、字段缺失、空对象、空 `mcpServers` 的三态语义正确。
- 非 object server entry 报错。
- 缺少 `command` 和 `url` 的 server entry 报错。
- MCP 配置变更后 `openclaw_sandbox` agent 标记 `env_restart_required`。
- `Recreate` 后重新 provision，并把最新 MCP 写入 `openclaw.json`。
- API response 不泄漏 `env` value、header value、token。

前端测试：

- `AgentProfileModal` 只在 `openclaw_sandbox` 显示 MCP 区域。
- `AgentDetailPane` 对 OpenClaw agent 显示 MCP 摘要和编辑入口。
- 保存普通字段时不误提交 `mcp_config`。
- 替换 MCP 时提交 object。
- 清除 MCP 时提交 `null`。
- 保存并重建按顺序调用 update 和 recreate。

手工验证：

1. 创建 OpenClaw worker，带一个 stdio MCP server。
2. 检查宿主机 `~/.csgclaw/agents/<agent>/.openclaw/openclaw.json` 包含 `mcp.servers`。
3. 启动 agent 后确认 OpenClaw gateway 能看到 MCP tool。
4. 修改 MCP 配置并只保存，确认 UI 标记需要重建，旧 gateway 不被停止。
5. 点击重建，确认新 `openclaw.json` 生效，`env_restart_required` 清除。

## 8. 实施顺序

1. 增加后端 MCP 数据模型、持久化和 API 脱敏摘要。
2. 增加 MCP parser/validator 和 `openclawsandbox` 配置渲染。
3. 扩展 provision request，把 agent MCP 配置传到 OpenClaw runtime。
4. 增加 update 后标记 `env_restart_required` 的逻辑，但不触发 OpenClaw hot reload。
5. 接入前端 Agent 创建/详情页 MCP UI。
6. 增加 CLI `--mcp-config-file` 和 `--clear-mcp-config`。
7. 补充 `docs/config.zh.md`、`docs/api.zh.md`、`docs/cli.zh.md`。
