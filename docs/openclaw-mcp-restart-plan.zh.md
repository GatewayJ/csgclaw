# MCP 配置控制器实现说明

本文档记录当前分支对 CSGClaw MCP 配置接入的实现。旧方案把 OpenClaw 的 MCP 配置放在 `runtime_options.mcp`；当前实现已调整为独立的顶层 `mcp_config`，并在 runtime 接口层增加 `MCPConfigController`，由各 runtime adapter 负责把统一输入转换为自己的原生配置格式。

## 1. 目标与边界

已实现目标：

- API、持久化和前端使用统一的顶层 `mcp_config` 字段。
- `mcp_config` 的输入形态固定为 `{"mcpServers": {...}}`。
- runtime 层新增 `MCPConfigController`，与现有 start/stop/provision 和 runtime config controller 分离。
- OpenClaw、PicoClaw、Codex CLI 三类 agent 都接入 MCP server 配置。
- 前端创建、编辑和详情页使用同一份 MCP Server JSON 配置形态。
- 旧客户端提交的 `runtime_options.mcp` 会迁移到 `mcp_config`，并从 `runtime_options` 中剥离。

当前未覆盖：

- 不做 MCP marketplace、自动安装、连接测试或动态发现。
- 不承诺 runtime hot reload；需要重启/重建时仍走现有 runtime recreate 路径。
- 暂不做 MCP secret 脱敏体验，`mcp_config` 仍按普通 JSON 配置回显。

## 2. 统一配置形态

API 输入：

```json
{
  "runtime_kind": "openclaw_sandbox",
  "mcp_config": {
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
```

语义：

- `mcp_config` 字段缺失：不修改已有 MCP 配置。
- `mcp_config: null`：清除 CSGClaw 托管 MCP 配置。
- `mcp_config: {}` 或 `{"mcpServers": {}}`：显式托管空集合。
- 非空 `mcpServers`：使用 CSGClaw 托管 server 集合。
- 顶层仅支持 `mcpServers`，避免混入 runtime 原生 MCP 配置导致合并语义不清。

校验规则：

- `mcpServers` 必须是 object。
- server 名称去空白后不能为空，重复名称报错。
- 每个 server entry 必须是 object。
- 每个 server entry 必须声明 `command` 或 `url`。
- `args` 必须是 string array。
- `env`、`headers` 必须是 string map。
- `transport` 必须是 string。

## 3. Runtime 接口

共享接口定义在 `internal/runtime/mcp_config.go`：

```go
type MCPConfigController interface {
    ValidateMCPConfig(ctx context.Context, current MCPConfigSnapshot) error
    MCPConfigRestartRequired(change MCPConfigChange) (bool, error)
    ReconcileMCPConfig(ctx context.Context, h Handle, change MCPConfigChange) error
}
```

它的职责：

- 校验当前 runtime 是否支持 `mcp_config` 以及配置是否符合该 runtime 的约束。
- 判断 MCP 配置变更是否需要 runtime recreate。
- 对无需 recreate 的 runtime，或需要即时重写本地配置的 runtime，执行 reconcile。

`ProvisionRequest` 也新增了独立字段：

```go
type ProvisionRequest struct {
    MCPConfig map[string]any
}
```

这样 MCP 不再通过通用 `RuntimeOptions` 传递，runtime options 和 MCP 配置的生命周期可以独立演进。

## 4. Runtime Adapter 映射

### OpenClaw

OpenClaw 接收统一 `mcp_config.mcpServers`，渲染到 OpenClaw 原生配置：

```json
{
  "mcp": {
    "servers": {
      "context7": {
        "command": "uvx",
        "args": ["context7-mcp"]
      }
    }
  }
}
```

OpenClaw 配置文件路径仍是：

```text
~/.csgclaw/agents/<agent-id>/.openclaw/openclaw.json
```

sandbox 内读取路径：

```text
/home/node/.openclaw/openclaw.json
```

OpenClaw adapter 使用共享 JSON helper 更新 `mcp.servers`。MCP command 在 OpenClaw sandbox 内执行，filesystem server 参数必须是容器内可见路径。

### PicoClaw

PicoClaw 接收同样的 `mcp_config.mcpServers`，渲染到 PicoClaw config：

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "context7": {
          "command": "uvx",
          "args": ["context7-mcp"]
        }
      }
    }
  }
}
```

当 `mcp_config` 缺失时，adapter 会关闭 CSGClaw 托管 MCP：

```json
{
  "tools": {
    "mcp": {
      "enabled": false
    }
  }
}
```

### Codex CLI

Codex CLI 接收同样的 `mcp_config.mcpServers`，写入隔离 Codex home 的 `config.toml` 管理块：

```toml
# BEGIN csgclaw-managed mcp
[mcp_servers."context7"]
command = "uvx"
args = ["context7-mcp"]
env = { "CONTEXT7_API_KEY" = "secret" }
# END csgclaw-managed mcp
```

远程 server 会写入 Codex 支持的字段：

```toml
[mcp_servers."remote-search"]
url = "https://mcp.example.com/mcp"
bearer_token_env_var = "MCP_TOKEN"
oauth_client_id = "client-id"
oauth_resource = "resource"
```

Codex adapter 会将统一输入中的共享字段转换为 Codex TOML 语义，例如远程 server 的 `headers` 会写为 `http_headers`；`transport` 不作为 TOML 字段写入，Codex 通过 `url` 隐式使用 streamable HTTP。

## 5. API 与持久化

涉及字段：

- `agent.Agent.MCPConfig`
- `agent.CreateAgentSpec.MCPConfig`
- `agent.UpdateRequest.MCPConfig`
- `apitypes.Agent.MCPConfig`
- participant agent binding 中的 create payload
- Web `AgentLike.mcp_config` / `AgentDraft.mcp_config`

创建 agent：

```json
{
  "name": "alice",
  "runtime_kind": "picoclaw_sandbox",
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

更新 agent：

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

清除 MCP：

```json
{
  "mcp_config": null
}
```

兼容逻辑：

- create/update 收到旧 `runtime_options.mcp` 时，会迁移到 `MCPConfig`。
- 迁移后保存的 runtime options 不再包含 `mcp`。
- 如果同一次请求同时提交 `mcp_config` 和 `runtime_options.mcp`，以显式 `mcp_config` 为准。

## 6. 保存与重建

更新 `mcp_config` 的流程：

1. 解析请求并迁移 legacy `runtime_options.mcp`。
2. 通过对应 runtime 的 `MCPConfigController.ValidateMCPConfig` 校验。
3. 持久化新的 `Agent.MCPConfig`。
4. 通过 `MCPConfigRestartRequired` 判断是否需要 recreate。
5. 需要 recreate 时，设置 `env_restart_required = true`，再调用现有 `Recreate`。
6. recreate 成功后，新 runtime provision 使用最新 `MCPConfig` 生成原生配置，并清除 `env_restart_required`。
7. recreate 失败时，已保存的 `mcp_config` 保留，`env_restart_required` 继续为 true，用户可通过现有重建动作重试。
8. 如果 runtime 支持无需重建的配置同步，则通过 `ReconcileMCPConfig` 完成。

当前 OpenClaw、PicoClaw 和 Codex 的 MCP 配置变更都按需要重建处理，Codex 同时实现了 `ReconcileMCPConfig` 用于重写隔离 Codex home 配置。

## 7. 前端行为

前端保留现有 MCP JSON 编辑器组件，但读写对象改为 `draft.mcp_config`。

展示位置：

- 创建 agent modal 的 runtime section。
- Agent 详情页 runtime section。

展示条件：

- `openclaw_sandbox`
- `picoclaw_sandbox`
- `codex`

保存行为：

- 创建 agent 时，非空 MCP JSON 写入 create payload 的 `mcp_config`。
- 编辑 agent 时，MCP JSON 变化写入 update payload 的 `mcp_config`。
- 清除配置时，update payload 写入 `mcp_config: null`。
- `runtime_options` 和 `mcp_config` 独立比较、独立提交，避免 MCP 编辑覆盖其它 runtime option，例如 Codex 的 `local_workspace_dir`。

## 8. 验证

已覆盖的后端测试重点：

- `mcp_config` 缺失、空对象、空 `mcpServers`、非空 `mcpServers` 的语义。
- legacy `runtime_options.mcp` 迁移。
- OpenClaw/PicoClaw 接受 MCP 配置。
- Codex 生成 `[mcp_servers."<name>"]` 配置块。
- MCP 配置变更触发 recreate，失败时保留 restart required 状态。

已覆盖的前端测试重点：

- 模型层解析、保存和清除 `mcp_config`。
- OpenClaw/PicoClaw/Codex runtime 都展示 MCP 配置入口。
- Agent 创建/编辑 payload 使用顶层 `mcp_config`。
- MCP 编辑不覆盖普通 `runtime_options`。

本分支使用过的验证命令：

```bash
go test ./internal/runtime/openclawsandbox ./internal/runtime/picoclawsandbox ./internal/runtime/codex ./internal/agent ./internal/api ./internal/apitypes
pnpm exec vitest run tests/models/agents.test.ts tests/components/AgentProfileModal.test.tsx tests/components/AgentActions.test.tsx
pnpm exec tsc --noEmit
```
