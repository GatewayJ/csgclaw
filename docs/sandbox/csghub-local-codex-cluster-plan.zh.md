# CSGHub CSGClaw server 镜像启用本地 Codex：最小改造

## 目标与结论

目标是在 CSGHub 创建的 **一个 `csgclaw-server` 容器**中运行：

```text
csgclaw serve
├─ Codex manager app-server
└─ Codex worker app-server(s)
```

实现改动集中在 `sandbox-runtime` 的四个发布相关文件：Dockerfile、默认 worker
模板配置、构建默认基座版本和镜像发布 tag。**不修改 CSGClaw Go 代码、CSGBot、
entrypoint、hub 模板或 `csgclaw-agent` 镜像。**

理由是 CSGClaw 的 `codex` 已是本地 runtime：它在 CSGClaw 所在环境直接执行
`codex app-server --listen stdio://`。因此 CSGHub server sandbox 只要具备相同二进制
和默认模板，运行行为与个人电脑部署一致。`runtime_kind=codex` 不会进入
sandbox gateway，也不会为 manager/worker 调 CSGHub `Create`。

旧 PicoClaw/OpenClaw sandbox capability 保留，但不是这个 server 镜像的默认
manager/worker 路径。

## 改动清单

### 1. `csgclaw-server/Dockerfile`

只做三项：

1. 将 `CSGCLAW_BASE_IMAGE` 更新为
   `opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/csgclaw:v0.3.18`。
2. 在镜像构建期用多阶段构建安装**固定版本**的 Linux Codex CLI：
   - 写入 `/usr/local/bin/codex`；
   - 固定 npm 包 `@openai/codex@0.144.1`，按 `TARGETARCH` 复制其 musl 原生
     二进制；
   - 设置 `CSGCLAW_CODEX_PATH=/usr/local/bin/codex`（或确保该路径在 `PATH`）；
   - 构建期运行 `codex --version`。
3. 将基座中直接位于 `/usr/local/bin` 的 `csgclaw` / `csgclaw-cli` 规范化为
   `/opt/csgclaw/bin/...` 的官方 bundle 布局，并保留原命令路径的软链。
   `v0.3.18` 基座本身没有这个布局，而正式版本的 `csgclaw serve` 会校验它；不做这
   一步会在启动前直接退出，与 Codex 无关。

构建期的 npm 和 Alpine 下载源均是可覆盖的 Docker `ARG`；仅影响构建下载速度，
不改变容器运行时的软件源或 CSGClaw 行为。

不要依赖 `csgclaw serve` 的运行期自动下载；它失败只会告警，可能造成 HTTP server
健康而 Codex manager 不可用。

以下 Dockerfile 内容不改：

- UID/GID 1000 和 `/home/picoclaw/.csgclaw` PVC 布局；
- supervisor、tini、Python sandbox :8888；
- `CSGCLAW_AGENT_TEMPLATE_IMAGE`、hub 模板的 image 替换；
- `csgclaw-agent` 相关构建能力。

### 2. `csgclaw-server/config.toml`（仅控制默认新 worker）

只替换 worker 默认模板：

```toml
[bootstrap]
default_worker_template = "builtin.codex-worker"
```

不需要修改 `default_manager_template`。实际 manager 在
`internal/agent/service.go:465-488` 中固定为 Codex，不从这个配置选择 runtime。

`default_worker_template` 也不改变已有 worker，更不改变用户显式选择的模板。它只在
创建请求没有 `from_template` 时被 `templateRefForCreateSpec` 作为 worker 默认模板
（`internal/agent/service.go:1028-1045`）使用。当前 server 镜像写的是
`local/picoclaw-worker`，所以普通“新建 worker”会默认走 PicoClaw sandbox；改成
`builtin.codex-worker` 才使这个默认操作走容器内 Codex。

若前端/API 始终显式传 `from_template = "builtin.codex-worker"`，则连这一个
`config.toml` 改动也不需要。

其余配置行保持原样，尤其是：

```toml
[server]
advertise_base_url = "${CSGCLAW_ADVERTISE_BASE_URL}"

[sandbox]
provider = "csghub"
```

不能将 `advertise_base_url` 固定为 `127.0.0.1`。当前 CSGClaw 同时用它：

- 给 Codex profile 构造 CSGClaw LLM bridge URL；
- 给独立 sandbox 中的 OpenClaw/PicoClaw 生成 `CSGCLAW_BASE_URL` 回调地址。

OpenClaw/PicoClaw 若在另一个容器，`127.0.0.1` 是它自己的 loopback，无法回调
server。最小方案沿用 CSGBot 注入的外部可达 Gateway URL；Codex 也通过这个已有地址
访问 CSGClaw LLM bridge。

### 3. 明确不改 `docker-entrypoint.sh`

entrypoint 当前每次启动都会把镜像内的 `config.toml` 和 `hub/` 复制进 PVC，并要求
现有的模型/CSGHub 环境变量。最小方案不改变这些行为。

这也意味着：必须修改并发布镜像内的 `config.toml`；在运行中手改 PVC 的 config 会被
下一次启动覆盖。

### 4. 构建与发布默认值

`Makefile` 中的 `CSGCLAW_BASE_IMAGE_NAME` 同步为 `v0.3.18`，避免本地 `make`
仍构建旧基座；`.ci/images/csgclaw-server-sandbox.env` 将待发布镜像标为：

```text
opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsg_public/csgclaw-server-sandbox:20260713-dev
```

这两个文件不改变线上运行时逻辑，只使默认构建和 CI 发布指向本次镜像。

## 不修改的仓库和文件

| 路径/仓库 | 原因 |
| --- | --- |
| `csgclaw/` | 本地 Codex runtime、session/bridge 和 CSGHub gateway provider 都已具备。 |
| `csgbot/` | 继续创建外层 server sandbox，并继续提供当前的环境变量、PVC、健康检查和旧 sandbox 配置。 |
| `csgclaw-server/docker-entrypoint.sh` | 当前配置复制和环境变量校验可继续使用。 |
| `csgclaw-server/hub/` | 默认路径改为 builtin Codex template，不需要修改 local hub。 |
| `csgclaw-agent/` | 保留旧 OpenClaw/PicoClaw sandbox capability；默认 Codex 路径不会启动它。 |

## 验收

1. 构建新 server 镜像；以 `--entrypoint /usr/local/bin/codex` 运行
   `codex --version`，确认 CLI 在最终镜像可执行。
2. 使用现有 CSGBot 创建 CSGClaw server sandbox，确认容器用户可写
   `/home/picoclaw/.csgclaw`。
3. 确认 manager 为 `runtime_kind=codex`；未显式选择模板的新建 worker 也为
   `runtime_kind=codex`（若未改 worker 默认模板，则显式选择 `builtin.codex-worker`）。
4. 观察 CSGHub 审计：创建上述 manager/worker 时不应产生新的 sandbox `Create`；
   仅有 CSGBot 创建的外层 server sandbox。
5. 发送一次真实消息，确认 Codex 能通过现有
   `CSGCLAW_ADVERTISE_BASE_URL` 到达 CSGClaw LLM bridge 并完成响应。

若第 5 步失败，先检查镜像中 Codex 的 stderr、AI Gateway 的 Responses 接口和
`CSGCLAW_ADVERTISE_BASE_URL` 的容器可达性；这些不是 CSGClaw runtime 重写问题。
