# 方案文档：Agent 仅通过 ServiceOptions 注入 Sandbox Provider

## 1. 背景与问题

当前 `agent` 层已经引入了较多 BoxLite/CSGHub 分叉逻辑（backend、manager 生命周期差异实现、部分 env 读取路径差异）。  
目标是将后端差异尽量下沉到 `internal/sandbox/*`，让 `internal/agent` 保持接近 `main` 分支的通用业务形态。

本方案要求：

1. 后端选择只发生在 `cli/sandboxopts.ServiceOptions(...)`。
2. `agent` 层只依赖 `sandbox.Provider/Runtime` 抽象，不直接感知 `csghub` 类型。
3. `csghub` 专属 env 在 `internal/sandbox/csghub` 内读取，不由 `agent` 读取。

---

## 2. 目标架构

### 2.1 模块边界

- `cli/sandboxopts/*`
  - 唯一后端注入点。
  - `!csghub`：boxlite / boxlite-cli 二选一。
  - `csghub`：显式注入 `csghub.Provider`。

- `internal/agent/*`
  - 负责业务语义：manager 唯一性、agent 注册表、权限/角色、workspace/config 生成、状态持久化。
  - 调用统一 `runtime.Create/Get/Remove/Run`。
  - 不读取 csghub 专属 env，不依赖 `*csghub.Runtime`。

- `internal/sandbox/csghub/*`
  - 负责运行时差异：Hub API、状态机、重试、等待 Running、错误映射、日志流。
  - 负责读取 csghub 专属 env（或内部 `LoadEnv`）。

- `internal/sandbox/sandbox.go`
  - 保持后端无关抽象，不反向依赖具体后端包。

### 2.2 调用关系

`serve/onboard -> sandboxopts.ServiceOptions -> agent.NewService(...) -> provider.Open(...) -> runtime.Create/Get/Remove`

---

## 3. 配置与环境变量流转

## 3.1 共性配置（config.toml）

- `config.toml` 仍是业务配置基线（server/llm/channels/sandbox 基础项）。
- `serve` 启动时：先自动初始化（仅首次），再读配置，再叠加运行时覆盖。

## 3.2 后端注入

- `ServiceOptions(cfg.Sandbox)` 决定注入哪个 provider。
- `agent` 构造函数不再“隐式默认”创建 csghub provider。

## 3.3 csghub 专属 env

- 例如 `CSGHUB_*`、`CSGCLAW_SANDBOX_*`、`CSGCLAW_PVC_MOUNT_PATH`、`CSGCLAW_SANDBOX_SUBPATH_ROOT`。
- 在 `internal/sandbox/csghub` 内部读取并校验。
- `agent` 不透传这类参数，避免边界污染。

---

## 4. 关键语义约束（必须保留）

1. `ForceRecreate` 语义保留（manager 需要可强制重建）。
2. manager 业务语义保留（唯一 manager、普通删除不可删 manager）。
3. `Delete` 幂等性保留（not found 视为成功）。
4. `CreateWorker/Create/StreamLogs` 外部行为保持兼容。

---

## 5. 设计选择

## 5.1 外层仅调用 Create

`agent` 外层只调用 `runtime.Create(...)`，不依赖 `Reconcile`。  
CSGHub 的“收敛到 Running”逻辑下沉到 `csghub.Runtime.Create()` 内部实现：

- get existing
- create/apply
- start（必要时）
- wait until Running/Ready（含 timeout/poll）

这样外层不需要感知后端生命周期差异。

## 5.2 ForceRecreate 处理

推荐在 `agent.EnsureManager(force=true)` 使用：

1. `runtime.Remove(managerIDOrName, Force=true)`  
2. 再 `runtime.Create(managerSpec)`

即保持老语义，不扩散新接口复杂度。

## 5.3 guest path 契约统一（boxlite/csghub）

`csghub` 与 `boxlite` 必须共享同一套容器内挂载点（guest path）契约，例如：

- `/home/picoclaw/.picoclaw`
- `/home/picoclaw/.picoclaw/workspace`
- `/home/picoclaw/.picoclaw/workspace/projects`

差异只允许存在于后端对同一字段的消费语义：

- boxlite: `HostPath` 按主机路径直接消费。
- csghub: `HostPath` 按 PVC subpath 直接消费（映射到 Hub `sandbox_mount_subpath`）。

因此，`CSGCLAW_PVC_MOUNT_PATH` 仍保留为 csghub 后端内部的部署参数，不上浮到 `agent` 通用层；
`agent` 层继续只表达“挂载意图 + 固定 guest path 契约”。

---

## 6. 分阶段实施计划

## Phase 1：注入点收口（低风险）

1. `service_options_csghub.go` 改为显式注入 `agent.WithSandboxProvider(csghub.NewProvider(...))`。
2. `agent.NewService...` 取消 csghub 默认 provider 构造逻辑。
3. 验证：`go test ./...`、`go test -tags csghub ./...`。

## Phase 2：env 读取下沉（中风险）

1. 新增 `internal/sandbox/csghub/env.go`（或等价 loader）。
2. 将 csghub 专属 env 读取从 `agent` 移出。
3. 保持 `agent` 输入为通用 spec，不持有 csghub params。

## Phase 3：Create 语义收敛（中风险）

1. `csghub.Runtime.Create` 内部实现 reconcile+wait。
2. `agent` 去掉对 `ManagedRuntime/Reconcile` 的依赖路径。
3. 回归测试覆盖 create/delete/stream logs/manager recreate。

## Phase 4：代码清理（低风险）

1. 删除冗余 backend 分叉（如果不再需要）。
2. 精简注释、死代码、测试钩子（仅删无引用项）。

---

## 7. 风险与阻塞点

## 7.1 风险

1. provider 未注入导致启动失败（unconfigured provider）。
2. `Create` 变阻塞后 timeout/poll 配置不合理引发假失败。
3. `ForceRecreate` 丢失导致 manager 无法按预期重建。
4. 挂载字段配置错误导致后端创建失败（由后端返回错误）。
5. not-found 分类不一致破坏 delete 幂等。

## 7.2 阻塞点（先决策）

1. `csghub` build 是否强制由 `ServiceOptions` 注入 provider（建议：是）。
2. `ForceRecreate` 走 `Remove+Create` 还是扩展 spec 标志（建议：Remove+Create）。
3. `Create` 的最终语义是否统一为“确保 Running”（建议：是）。

---

## 8. 验收标准

1. `agent` 不再读取 csghub 专属 env。  
2. `agent` 不依赖 `*csghub.Runtime` 或 csghub 私有类型。  
3. 后端注入只在 `cli/sandboxopts`。  
4. 全量测试通过：
   - `go test ./...`
   - `go test -tags csghub ./...`
   - `go list -deps ./... | rg -n "internal/sandbox/csghub|internal/sandbox/csghubsdk"` 在非 csghub 构建下应无结果
   - `go list -deps -tags csghub ./... | rg -n "internal/sandbox/boxlite|third_party/boxlite-go"` 在 csghub 构建下应无结果
5. manager/recreate/delete/logs 行为与现网预期一致。
6. boxlite/csghub 的 guest path 完全一致；`HostPath` 字段值按配置直通并由各后端直接消费。

---

## 9. 回滚策略

若 Phase 2/3 出现回归：

1. 保留 Phase 1（注入点收口）不回退。  
2. 回退 `Create` 收敛实现到旧路径（临时恢复 `Reconcile` 调用链）。  
3. 逐项恢复 env 读取路径并补测试后再推进。

---

## 10. 结论

该方案能在不牺牲 manager 业务语义的前提下，把后端差异集中在 sandbox 层，并让 `agent` 回归 `main` 风格。  
推荐按 Phase 1 -> 2 -> 3 小步推进，每步都跑双构建测试并可独立回滚。

---

## 11. 文件级改动清单（对应 Phase）

### Phase 1：注入点收口

1. [service_options_csghub.go](/home/jhw/opcsg/csgclaw/cli/sandboxopts/service_options_csghub.go)
- 从“返回 nil”改为显式注入 `agent.WithSandboxProvider(csghub.NewProvider(...))`。

2. [service_csghub.go](/home/jhw/opcsg/csgclaw/internal/agent/service_csghub.go)
- 去掉构造函数里的“默认创建 csghub provider”分支，只消费注入结果。

3. [service.go](/home/jhw/opcsg/csgclaw/internal/agent/service.go)（可选）
- 对齐 `unconfiguredSandboxProvider` 失败语义，保证 boxlite/csghub 构造行为一致。

### Phase 2：csghub env 下沉到 sandbox

1. 新增 [env.go](/home/jhw/opcsg/csgclaw/internal/sandbox/csghub/env.go)
- 集中读取/校验 csghub 专属环境变量。

2. [provider.go](/home/jhw/opcsg/csgclaw/internal/sandbox/csghub/provider.go)
- `Open/NewProvider` 改为依赖 csghub 内部 env loader。

3. [runtime.go](/home/jhw/opcsg/csgclaw/internal/sandbox/csghub/runtime.go)
- 不再依赖 agent 透传的 csghub params（改用 provider/runtime 内部配置）。

4. [service_csghub.go](/home/jhw/opcsg/csgclaw/internal/agent/service_csghub.go)
- 删除 `loadSandboxParams/csghubParamsFrom` 调用路径。

5. [env_csghub.go](/home/jhw/opcsg/csgclaw/internal/agent/env_csghub.go)
- 删除或显著瘦身（仅保留 agent 侧确实需要的逻辑）。

### Phase 3：Create 语义收敛（外层只调 Create）

1. [runtime.go](/home/jhw/opcsg/csgclaw/internal/sandbox/csghub/runtime.go)
- `Create` 内部完整实现 `get/create/apply/start/wait running`。

2. [service_csghub.go](/home/jhw/opcsg/csgclaw/internal/agent/service_csghub.go)
- 去掉对 `ManagedRuntime.Reconcile` 的直接依赖，统一走 `runtime.Create`。

3. [service_common.go](/home/jhw/opcsg/csgclaw/internal/agent/service_common.go)
- 如果仍存在 Reconcile 分支，收敛到 Create 路径。

4. [instance.go](/home/jhw/opcsg/csgclaw/internal/sandbox/csghub/instance.go)（可选）
- 保留 `CachedInfo` 零 round-trip 优化能力。

### Phase 4：行为统一与清理

1. [service_common.go](/home/jhw/opcsg/csgclaw/internal/agent/service_common.go)
- 统一普通 agent 的 `role/image` 校验策略。

2. [service.go](/home/jhw/opcsg/csgclaw/internal/agent/service.go)
3. [service_csghub.go](/home/jhw/opcsg/csgclaw/internal/agent/service_csghub.go)
- 统一 `BoxID` 的持久化/刷新时机。

4. 清理死代码/过时注释/仅测试残留钩子（先 `rg` 确认无业务引用再删除）。

### 每个 Phase 的验证命令

1. `go test ./...`
2. `go test -tags csghub ./...`

重点回归文件：

1. [service_test.go](/home/jhw/opcsg/csgclaw/internal/agent/service_test.go)
2. [service_csghub_test.go](/home/jhw/opcsg/csgclaw/internal/agent/service_csghub_test.go)
3. [adapter_test.go](/home/jhw/opcsg/csgclaw/internal/sandbox/boxlite/adapter_test.go)
4. [client_test.go](/home/jhw/opcsg/csgclaw/internal/sandbox/csghubsdk/client_test.go)

---

## 12. 定稿约束（本轮结论）

1. 路径统一方式
- 使用同一组挂载字段同时承载 boxlite host 路径与 csghub PVC subpath。
- 配置文件写什么，运行时就使用什么：不在 `agent` 层做语义转换，不在 provider/runtime 层做语义转换。
- 对该字段不增加额外格式校验；由后端接口按其原生协议处理并返回错误。
- `agent` 仅负责组装挂载项与固定 guest path 契约。

2. 生命周期封装
- `csghub` 生命周期完全封装在 `internal/sandbox/csghub`（provider/runtime）内部。
- 对 `agent` 仅暴露 `Create/Get/Remove/Run` 抽象。
- `Create` 内部负责 `create/start/wait ready`，`agent` 不感知 csghub 生命周期细节。

3. csghub 专属 env
- `CSGHUB_*`、`CSGCLAW_PVC_MOUNT_PATH` 等专属环境变量只在 `internal/sandbox/csghub` 内读取。
- `agent` 与通用 `sandbox` 抽象层不读取、不透传 csghub 专属 env。

4. build tag 边界
- build tag 主要出现在 `internal/sandbox/csghub/*` 与注入文件（如 `cli/sandboxopts/*_csghub.go`）。
- 非 csghub 构建不得引入 csghub sdk 依赖链，避免编译串味。
