# CSGClaw 飞书连接器 CLI + Skills 技术方案

## 1. 需求范围与分析依据

- 需求：[#3918 优化连接器连接和使用体验](https://git-devops.opencsg.com/product/agentichub/requirements/-/work_items/3918)，包含截至 2026-10-10 的全部非系统评论。

### 1.1 必须交付的能力

| 编号 | 需求 | 验收结果 |
|-|-|-|
| R1 | 连接器支持多种接入形态，飞书采用 CLI + Skills | 每个 CSGClaw 实例只有一个全局飞书连接器，仅配置一个飞书机器人；连接器模型兼容现有 MCP |
| R2 | 用户无需填写 App ID、App Secret、MCP 地址 | 按照官方 CLI 默认流程完成应用设置和个人登录；已有有效 CLI 配置直接复用 |
| R3 | 机器人和个人身份均可使用 | 显示两种身份各自的授权状态；Agent 按官方 Skills 和命令要求选择 `--as bot` / `--as user` |
| R4 | 用户与租户均只申请免审权限 | 应用注册显式指定 tenant、user 权限清单；个人登录使用 `auth login --scope` 传入用户权限清单，界面展示实际申请和获批权限 |
| R5 | 渠道与连接器独立 | 连接器页面负责 lark-cli 自动安装、配置和授权；渠道页面仅管理消息渠道，渠道生命周期不调用 lark-cli |
| R6 | worker 独立开启连接器并获得 Skills | worker 在 profile 的连接器页面开启全局飞书连接器；对应 Skills 完整复制到该 worker 的技能目录，并标明来源 |
| R7 | 已有 lark-cli 配置自动迁移 | 随机选择一份历史 lark-cli 配置作为全局配置，升级前已对接飞书的 worker 自动开启该连接器；渠道 IM 配置保持原状 |

本文中的 worker 对应现有代码和 API 中的 Agent，代码标识符继续使用 Agent。全局连接器保存唯一机器人的配置及对应个人授权，各个 worker 独立保存开启状态并共享全局配置。

### 1.2 官方 CLI 行为与页面职责

连接器页面直接对接 lark-cli，应用设置、个人登录和账号选择遵循所安装官方 CLI 的默认能力。全局连接器首次配置使用官方提供的非交互初始化入口；已有有效 CLI 配置和授权直接复用。浏览器中的具体设置步骤由飞书页面提供。

连接器页面负责 CLI 安装、应用设置、个人授权、状态检查和修复。渠道页面负责消息接收与发送，以及渠道应用配置；渠道新增、修改、删除和启动流程均不调用 lark-cli。

“最多两个授权阶段”指应用设置与个人授权。企业管理员审批、飞书登录验证等外部步骤遵循飞书规则，界面显示等待原因。已有 lark-cli 配置按第 9 节自动迁移到连接器。

### 1.3 本次范围

交付对象为现有支持 lark-cli 的 Codex、DSH host runtime，包括桌面运行和服务端 CSGClaw 实例。二进制安装在实际执行 Agent 命令的主机；浏览器可以位于另一台设备。其他 runtime 按能力检测显示支持情况。

本次包含安装、授权、绑定、Skills、状态恢复、断开、Agent 删除清理和迁移。其他服务的 CLI 适配、连接器市场、BoxLite 执行环境适配以及全部 MCP 服务的替换，属于独立需求。

## 2. 现有机制与复用位置

下列路径均相对于上述 CSGClaw commit。

| 现有位置 | 已确认行为 | 本次处理 |
|-|-|-|
| `internal/apps/types.go`、`resources.go` | 全局资源保存配置和凭证，Agent binding 保存 `ResourceID`、启用状态和连接意图 | 保留资源与绑定模型，增加接入形态、CLI 状态和飞书全局唯一限制 |
| `internal/apps/catalog.go`、`connector.go` | 连接器定义、配置校验和连接流程以 MCP 为中心；飞书需要应用凭证和 MCP 配置 | 按 `kind` 分发配置校验、连接、状态检查和释放逻辑 |
| `internal/api/connectors.go:347` | `handleConnectorOAuthStart` 返回不支持 OAuth | 接入资源级授权会话，并将 Agent 入口转向资源授权 |
| `internal/api/feishu_registration.go` | 已有注册申请、轮询、凭证接收；完成后写入 Participant，并自动初始化 lark-cli | 保留渠道注册流程，移除其中的 lark-cli 调用；连接器通过官方 CLI 完成应用设置 |
| `internal/channel/feishu/larkcli/source.go` | `Source.Resolve` 从当前 Agent 的 Participant 解析应用，检查归属 | 新增连接器 Source，通过 Agent binding 解析 CLI 账号引用；旧 Source 用于迁移识别及失败记录的兼容恢复 |
| `internal/runtimeextension/larkcli/driver.go` | 检测二进制、生成私有配置、执行 `config bind`、观察配置版本；默认 `bot-only` | 复用 Driver 生命周期，引用 CLI 管理的账号配置，增加安装路径、全局配置目录引用和授权后状态更新 |
| `internal/agentengine/runtime_extension_manager.go`、`runtime_adapter.go`、`internal/runtime/extensionstate` | 已有配置准备、启用和 runtime 更新；Source 变更失败会清理旧配置，启用后的 Cleanup 会删除旧版本目录 | 连接器更新通过 Engine 内部入口组织停止、配置更新与启动，复用 Driver 和 projection；历史迁移保留恢复材料，延迟清理至运行验证完成 |
| `internal/skill/local/upload.go` | `InstallArchiveWithOptions` 校验并安装技能目录 | 安装官方 Skills 到全局资源 |
| `internal/agentengine/agents/skills.go`、`workspace_skills_transaction.go` | 已有目录校验、复制和生命周期保护；ReplaceSkills 从全局来源重建整个集合 | 复用校验与生命周期保护，仅更新连接器拥有的目录，保存其他目录原有内容 |
| `internal/runtime/codex/extensions.go`、`internal/runtime/dsh/extensions.go` | Codex、DSH 已实现 ExtensionDriver 和指令加载 | 继续使用对应 Runtime Adapter |
| `web/app/src/components/business/Apps` | 已有全局连接器页面、Agent 绑定页面和配置表单 | 全局页面配置唯一飞书机器人；worker profile 提供独立开启开关 |

两个现有事实需要保留在设计中：

- `internal/connectors` 主要承担 GitHub / GitLab 账号认证；页面中的应用连接器资源归 `internal/apps` 管理。本次资源模型继续位于 `internal/apps`。
- CSGClaw 的 exec secret provider 在 `config bind` 时提供 App Secret。官方 CLI 随后将其保存到自身 keychain；后续命令可能使用缓存凭证。因此，撤销 provider token 不能单独代表 CLI 调用已停用。[官方 binder 源码](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/cmd/config/binder.go)

## 3. 业内实现依据与设计选择

| 参考 | 可复用的做法 | CSGClaw 采用方式 |
|-|-|-|
| [WorkBuddy 连接器规范](https://open.workbuddy.cn/docs/connector) | 接入类型、安装、授权、状态检查、退出授权分别定义；CLI 配套 Skills；支持分阶段认证 | `kind=mcp/cli`；飞书提供固定安装包与授权流程；登录状态由 CLI 管理 |
| [飞书官方 CLI](https://github.com/larksuite/cli/tree/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d) | 官方命令、Skills、Device Flow、身份选择与凭证刷新 | 直接执行官方二进制，保持业务命令和返回格式 |
| [飞书 Agent 接入指南](https://open.feishu.cn/document/mcp_open_tools/feishu-cli/embed-feishu-cli-in-agent.md) | 凭证和命令控制具有独立扩展接口 | 使用官方配置与认证命令；全局资源引用和 worker 开关由 CSGClaw 的 runtime 生命周期处理 |
| [OAuth Device Flow，RFC 8628](https://www.rfc-editor.org/rfc/rfc8628.html) | 浏览器与 CLI 分离，按授权端要求轮询，处理超时和拒绝 | 后端持有授权会话，前端展示链接和状态；关闭页面不终止有效会话 |

CSGClaw 负责资源、安装、绑定与状态；lark-cli 负责应用设置、个人登录、飞书业务调用、Token 刷新与凭证存储。连接器页面调用官方 CLI，渠道页面沿用现有消息渠道注册与配置流程。

## 4. 资源模型与模块职责

### 4.1 对现有模型的扩展

以下字段为设计新增，具体 Go 类型遵循当前 `internal/apps` 风格。

| 对象 | 新增内容 | 保存原则 |
|-|-|-|
| `Definition` | `kind` | 目录 API 描述接入类型、展示信息和授权方式；CLI 版本由安装器提供 |
| 全局 `Installation` | `kind`、`package_version`、`skill_version`、`auth_account_ref`、`cli_config_dir`、`revision`、`auth_status` | 继续使用全局资源记录；列表仅返回脱敏身份和状态 |
| Agent binding | `applied_revision`、`skills` | 继续引用 `ResourceID`；复用 `Installation.LastErrorCode` 及现有错误处理；不复制长期凭证 |
| `AuthSession` | `session_id`、`resource_id`、`stage`、`status`、`expires_at`、`resource_revision` | 敏感轮询信息仅保存于后端私有目录 |
| `binding.skills[]` | `skill_name`、`bundle_version`、`content_digest`、`managed` | 随 binding 保存于 `app_installations`，通过其 `ResourceID` 关联来源 |

旧记录缺少 `kind` 时按 `mcp` 读取；新建飞书资源明确写入 `cli`。`Config.Transport` 继续描述 MCP 的 HTTP / stdio，CLI 使用独立配置结构。CLI 的配置校验不要求 MCP URL、工具发现或 MCP session。

内置包继续使用 `plugin.json` 和 `apps.json`。`plugin.json` 保存名称、说明、图标等展示信息，`apps.json` 引用内置服务。飞书目录由这些展示信息与 CLI 类型、Device Flow 授权方式组合生成。飞书 CLI 版本、Skills commit、平台下载地址及 SHA-256 由 `internal/larkcli` 安装器统一管理，飞书 `plugin.json` 不保存 CLI 版本。

安装器根据主机平台下载并校验官方二进制与 Skills，保存到 `package/<version>/bin` 和 `package/<version>/skills`，并生成 `package.json`，记录 `version`、`skills_commit`、`executable_sha256`、`skills_sha256`。`Installation` 保存已安装版本和授权状态；worker binding 引用全局资源，安装对应 Skills 并配置 CLI 路径及共享配置目录。当前支持 Codex、DSH host runtime。

`Manifest.Skills` 保留为原始 JSON 元信息；飞书 Skills 由专用安装器处理。当前连接器目录仅加载程序内置包，自定义 CLI 包格式和上传安装入口尚未提供。

历史 MCP 记录继续按现有规则加载，保留显式 `kind=mcp` 创建、配置校验、凭证处理、连接和迁移路径。

每个 CSGClaw 实例只有一个全局飞书连接器，配置一个飞书机器人，对应一个 App ID。全局页面固定展示飞书连接器入口，首次配置时创建唯一资源记录，未配置时显示待配置状态。`internal/apps` 在资源写入的同一临界区检查飞书资源唯一性；重复或并发初始化复用该记录及授权会话。已断开的资源仍保留该唯一记录。

每个 worker 在 profile 的连接器页面独立开启或关闭飞书连接器。继续复用现有 Agent binding 保存 `ResourceID`、`Enabled` 和连接意图，每个 worker 最多一条飞书 binding，全部引用同一个全局资源。首次开启创建 binding，后续开关复用该记录；新 worker 默认关闭。全局连接已配置且该 worker 开启时，按第 6.3 节生成运行环境。机器人及个人授权设置在全局连接器页面统一管理。

### 4.2 模块分工

- **`internal/apps`**：资源、绑定、接入类型分发、授权会话、资源 revision、恢复和迁移记录。现有 MCP 逻辑与 CLI 逻辑共用资源入口。
- **CLI 授权适配器**：运行官方配置、登录、状态和退出命令，解析授权地址与结果，将进程状态转换为连接器会话状态。渠道注册代码继续归渠道模块管理。
- **连接器 Source**：注册 `Source.Provider=connector-feishu`，根据 Agent ID 及第 9.2 节的操作阶段解析 binding，生成临时运行参数。Source 不执行下载和浏览器授权。
- **`internal/runtimeextension/larkcli`**：生成运行环境、提供官方 CLI 路径、观察配置加载状态；复用 Extension 生命周期。
- **`internal/skill` 与 Agent Skills Controller**：安装全局技能包，并按来源记录更新 Agent 中由连接器管理的技能目录。
- **API / Web UI**：创建会话、展示状态和用户操作，不保存 App Secret、user token 或 refresh token。

连接器服务通过已有装配入口调用 Agent Engine；Runtime Driver 不回调渠道配置。网络授权期间不持有 Agent 生命周期锁；准备就绪后，检查 resource revision 并执行配置更新。

## 5. 连接流程与授权管理

### 5.1 用户流程

全局配置与 worker 开启分别保存在现有资源及 binding 中。历史配置迁移按第 9 节执行；被选中的历史配置只有 bot 时继续仅使用 bot，不进入个人授权阶段。

1. 用户在“资源 → 连接器”的飞书入口配置唯一机器人。后端创建或读取唯一全局资源，自动安装经过版本锁定的 CLI 和官方 Skills，页面显示进度及重试操作。
2. 后端检查全局 CLI 配置。已有有效配置时直接复用；未配置时执行官方应用初始化命令，展示 CLI 返回的飞书页面地址。
3. 应用配置完成后进入个人登录阶段，展示飞书授权链接和二维码；已有有效个人授权时跳过此阶段。后端验证身份状态并保存全局授权结果。
4. 用户在各个 worker 的 profile → 连接器页面开启“飞书”。页面展示同一个全局机器人的名称、身份状态及该 worker 的开关；全局配置尚未就绪时展示前往全局设置的入口。
5. 开启后创建或启用该 worker 的 binding，复制官方 Skills 及 reference，配置 RuntimeExtension。运行中的 Codex、DSH 按第 6.3 节重启，加载完成后显示“可使用”；停止的 worker 等待下次启动。关闭同样通过更新环境、Skills 及重启生效。

全局连接器详情展示机器人、个人账号、授权状态和使用它的 worker。全局配置完成后，各个 worker 独立开启，无需重复安装 CLI 或进行个人登录。全局配置操作保持各个 worker 原有开关状态。

连接器提供一个“连接飞书”入口，由同一连接会话连续引导“应用设置 → 个人授权 → 配置生效”。应用设置完成后自动准备个人授权地址，用户通过同一个全局入口继续操作。每个阶段复用已有有效配置，页面保留进度和继续操作入口。

一次点击启动配置流程，应用设置与个人登录分别使用官方命令和授权页面。用户完成飞书页面要求的登录与授权确认；权限申请使用第 5.3 节的免审清单。浏览器无法自动打开页面时，显示“继续个人授权”链接或二维码。

### 5.2 应用注册与个人登录

**应用设置**由后端在唯一全局连接器的稳定配置目录执行官方非交互入口：

```sh
lark-cli config init --new --brand feishu --lang zh
```

该命令输出浏览器地址并等待用户完成设置。后端保留子进程，提取允许的官方 HTTPS 地址供页面展示，直到命令退出；成功后读取脱敏的 CLI 状态。App Secret 由 CLI 保存，页面只显示应用名称和 App ID。应用设置不创建 Participant、不绑定渠道，也不启动渠道连接。[官方应用初始化实现](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/cmd/config/init.go)

上述命令使用第 7.1 节的全局配置目录。新资源在授权验证完成前保持待配置状态。执行时清除继承的 `LARK_CHANNEL` 及官方 CLI 用于识别 OpenClaw、Hermes workspace 的环境标记。已有配置通过状态检查复用。

**个人登录**由后端在该资源的受管理 CLI 配置下执行：

```sh
lark-cli auth login --no-wait --json --scope "<user_scopes>"
lark-cli auth login --device-code <device_code> --json
lark-cli auth status --json
```

`<user_scopes>` 由 CSGClaw 将内置 user 清单用空格连接后作为一个命令参数传入。

第一条命令产生授权地址和 `device_code`；后端只向前端返回展示所需的地址、会话 ID 和有效期。第二条命令由后端任务等待完成。页面轮询 CSGClaw 的会话接口，不直接轮询飞书。调用参数、输出和日志经过敏感字段过滤。[官方登录实现](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/cmd/auth/login.go)

`auth status` 的退出码为零不代表个人授权成功；适配器必须检查 JSON 中的 `identities.user`、`identities.bot` 和账号标识。首次连接完成时增加 `auth status --verify --json` 验证；普通页面刷新读取缓存状态，避免反复请求飞书。[官方状态实现](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/cmd/auth/status.go)

### 5.3 权限与身份

CSGClaw 使用内置指定权限清单，以用户与租户均无需管理员审核为目标。清单保存在 `internal/apps/builtin/feishu/cli.json` 的 `init[].url_rewrite.gzip_query.addons.scopes` 中，分别维护 `user` 和 `tenant`；`requested_scopes` 声明个人授权申请范围。当前版本包含用户权限 133 项、租户权限 119 项；完整标识符见第 11 节。权限选择以对应身份的飞书授权页面和应用变更页面为依据，同时核查数据权限范围；官方推荐标记仅作为候选参考。CLI 升级和远程目录更新不会自动扩大申请范围。

应用注册由官方 `config init --new --brand feishu --lang zh` 发起。CSGClaw 将授权地址转换为官方支持自定义权限的 `/page/launcher`，设置 `from=sdk`、`source=csgclaw`、`tp=sdk`，通过 `addons.preset=false` 和 `addons.scopes.tenant/user` 指定权限。`addons` 按官方协议采用 JSON、gzip、无填充 base64url 编码；设备授权轮询和凭据保存由官方 CLI 完成。个人登录通过 `--scope` 传入同一份 user 清单。恢复未完成授权时，记录的申请权限必须与当前清单一致，否则重新生成授权请求。

`offline_access` 由官方 CLI 在个人登录时额外添加，用于获取和使用 refresh token。用户完成授权后，访问凭据到期时，CLI 可以自动换取新的访问凭据，使 CSGClaw 持续使用已获授权的功能，减少重复扫码。这里的 offline 表示用户无需一直停留在授权页面；访问飞书仍需网络。该权限不增加文档、消息等业务数据的访问范围。用户撤销授权或 refresh token 失效后，需要重新登录。

页面分别展示实际申请和获批权限；最终可执行能力受应用获批权限、个人授权和资源访问范围共同限制。已有应用的历史授权继续保留，修改申请清单不会自动撤销已有权限。`bot` 使用 `tenant_access_token`，`user` 使用 `user_access_token`。

身份选择遵循方案锁定版本的官方 CLI 行为，CSGClaw 不另外设置身份优先顺序：

1. 命令显式指定 `--as bot` 或 `--as user` 时使用指定身份；若违反 `strictMode` 限制，则返回错误。
2. 未显式指定 bot/user 时，`strictMode=bot/user` 决定使用对应身份。
3. 没有身份限制且未传入 `--as` 时，使用配置中的 `defaultAs`；取值为 `bot/user` 时使用对应身份。
4. `defaultAs=auto`、未设置默认身份，或显式指定 `--as auto` 时，按照 CLI 的凭证状态判断：个人 token 有效或能够刷新时选择 `user`；没有个人账号、没有个人 token 或 token 状态为 `expired` 时选择 `bot`。显式 `--as auto` 仍受 `strictMode` 约束。

首次配置全局连接器沿用官方默认 `strictMode=off`、`defaultAs=auto`；两个身份均支持某项操作且个人授权可用时，默认选择 `user`。具体命令仅支持一种身份或任务明确要求某种身份时，Agent 根据官方 Skills 显式传入对应的 `--as`。CLI 会检查所选身份是否受到命令支持；身份不支持、权限不足或调用失败时，CSGClaw 不自动改用另一身份重试。同一次调用只使用所选身份的权限。[官方身份选择实现](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/internal/cmdutil/factory.go)、[官方凭证状态判断](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/internal/credential/credential_provider.go)

明确要求个人身份的操作在个人授权失效时返回 `authorization_required`。`auto` 在执行请求前根据凭证状态选择 bot，属于官方默认选择行为；界面和执行结果保留实际使用的身份。CSGClaw 补充当前资源名称、已授权身份、账号选择方式和配置入口，官方 Skills 保留各命令的身份和权限说明。

历史配置保留所选记录的应用、账号及身份设置：`bot-only` 继续使用 bot；user 或 auto 保留原策略，token 过期仅影响授权状态。迁移不发起个人授权，具体流程见第 9 节。

历史用户需要增加个人能力时，在全局飞书连接器中为当前机器人完成个人授权。授权成功并验证后更新该资源的身份设置与 revision，保持 worker 的 ResourceID 和开关状态；页面展示受影响的 worker。授权取消或失败时保留原机器人配置和身份设置，已有 bot 调用能力继续可用。

当前渠道历史附件恢复指令只能在全局连接器应用与消息所属渠道应用一致时启用；应用不一致时不注入该指令。

### 5.4 会话与错误状态

授权会话状态为 `installing → app_authorization → user_authorization → applying → completed`，已有步骤满足时直接跳过；终止状态包括 `cancelled`、`expired`、`denied`、`failed`。

同一资源同时只有一个有效授权会话。重复点击返回原会话；每次结果提交检查 `session_id + resource_revision`。取消授权、断开连接或替换账号时，持久化取消意图并使旧会话失效，按第 6.3 节停止受影响 runtime，终止关联管理 CLI 子进程及其子进程组并等待退出，然后重新读取配置和账号状态。CLI 可能在收到取消信号前已经写入配置或凭证，必须核对并恢复一致状态后，才能启动下一次管理操作或恢复 worker 调用。无法确认进程退出或恢复失败时保持 runtime 停止或使用未配置环境，显示具体原因并允许重试。

后端重启后，恢复任务检查操作记录、CLI 已保存配置及残留进程；残留进程身份使用进程启动标识与操作 ID 核对，不能只使用 PID。确认旧进程结束后继续校验与恢复。已经完成且校验通过的阶段直接继续，个人授权可用未过期的 `device_code` 恢复等待；应用初始化进程中断且未保存有效配置时，重新开始该步骤。网络授权等待期间不持有 Agent 生命周期锁；对稳定配置目录具有写入能力的 CLI 进程由全局管理操作独占，直到进程退出且配置检查完成。

| 情况 | 行为 |
|-|-|
| 用户关闭页面 | 会话继续有效；再次进入页面恢复展示 |
| `authorization_pending` / `slow_down` | 按飞书指定间隔继续或延长等待，不标记授权失败 |
| 用户拒绝或授权过期 | 结束该会话，保留已完成的应用配置；重试从个人授权开始 |
| 管理员审批尚未完成 | 显示等待审批及飞书入口；不要求填写凭证 |
| Token 可刷新 | 由官方 CLI 在调用时刷新；保持 refresh token 的更新一致性 |
| refresh token 失效 | 标记对应个人身份 `authorization_required`，保留机器人身份状态 |
| 权限不足 / 资源不可访问 | 展示具体权限或访问范围问题，不反复启动登录 |
| 网络失败 / 下载失败 | 保留可用版本与资源设置，允许重试；不重复执行业务写入命令 |

### 5.5 幂等重新配置

配置失败后的重新配置必须能够修复失败残留。唯一资源记录、配置目录或历史会话存在，仅表示已有管理记录；是否已配置必须根据当前应用、账号、凭证引用和各阶段校验结果判定。页面处于未配置、配置失败或待修复状态时，始终提供重新配置和刷新状态入口。刷新状态触发后端检查并更新展示；重新配置按当前检查结果执行恢复，用户无需手动删除文件或数据库记录。

未配置时重新配置，复用稳定的 ResourceID、配置目录和 worker binding，核对 CLI、Skills、应用设置、个人授权和运行配置。通过校验且符合本次配置目标的步骤直接复用；未完成或错误的步骤重新执行。临时安装文件、未完成操作记录、失效会话及损坏的受管理配置按归属处理；清理和重新写入均可重复执行。内容已被用户修改的 Skills 保留并报告冲突。旧账号有效凭证按其账号归属保留，仅在与目标 App ID、账号及身份设置匹配并通过验证后复用。

已经显示已配置但配置内容错误时，全局详情始终提供“断开连接”和“重新配置”。断开连接按第 7.2 节清除当前生效配置引用并返回待配置状态；随后重新配置使用用户本次选择的应用与账号，重新验证配置并生成 worker 运行引用。历史 completed 状态、旧缓存和残留文件不能跳过本次校验，也不能使错误配置自动恢复为已配置。

请求携带稳定的 `operation_id`、操作类型和目标 `resource_revision`。同一操作的重复请求返回现有进度或结果，不重复创建资源、binding、授权任务或安装目录；相同 operation_id 携带不同目标时返回冲突。失败操作重试从已记录阶段恢复；用户发起新的配置目标时使用新的 operation_id，并在旧操作结束及配置检查完成后执行。重试、刷新页面和服务重启均保留用户最新选择及 worker 开关意图。

## 6. CLI 安装、Agent 执行与 Skills

### 6.1 安装与版本

CSGClaw 直接从 `larksuite/cli` 官方 GitHub Release 下载与 OS / architecture 对应的预编译二进制包，当前固定版本为 `1.0.97`。下载后核对内置 SHA256，解压到临时目录，下载并校验固定 commit 的官方 Skills，验证文件摘要与 CLI 版本后启用。下载、安装与运行均无需 Node.js、npm 或本地编译工具。

CLI、Skills 来源 commit、校验值和最低兼容版本共同进入 CSGClaw 内置包清单。运行时不解析 `latest`。安装目录与授权数据目录分开；同一版本只下载一次，使用临时目录校验后原子启用。保留上一可用版本，升级失败继续使用它。

lark-cli 与 DSH 均由 CSGClaw 提供自动安装、进度展示和版本检查，分别使用 `internal/larkcli` 与 `internal/dshcli` 安装代码。lark-cli 直接下载原生二进制；DSH 通过 npm 安装 `@deepseek-ai/dsh`，需要 Node.js 和 npm，CSGClaw 会复用符合版本要求的环境，缺少时自动安装 Node.js 24。lark-cli 安装流程不修改用户 shell 配置。

lark-cli 默认安装位置为 `~/.csgclaw/apps/{resource_id}/package/{version}/bin/lark-cli`，Windows 可执行文件名为 `lark-cli.exe`，配套 Skills 位于同一版本目录的 `skills/`。二进制放在 Agent 实际执行命令的主机；授权配置单独保存于实例持久化目录。DSH 默认安装目录为 `~/.local/share/deepseek-harness`，启动入口位于 `~/.local/bin`。

### 6.2 RuntimeExtension 配置

保留每个 Agent 的 `feishu-lark-cli` Extension 名称和 `lark-cli` Kind，将 Source 改为 `connector-feishu`，`Ref` 为 Agent ID。正常运行时，Source 解析该 Agent 唯一已提交的飞书 CLI binding；历史迁移切换期间，依据第 9.2 节持久化操作记录中的阶段解析候选或原配置。runtime 启动及重启恢复使用同一解析规则。

Driver 生成连接器 ID、revision、官方 CLI 路径和 `LARKSUITE_CLI_CONFIG_DIR`，由 Runtime Adapter 在启动进程时注入。worker 开关、全局配置同步、连接器 Skills 更新和历史迁移统一通过第 6.3 节的 Engine 内部配置更新入口执行；迁移的配置恢复与清理规则见第 9.2 节。配置归属见第 7.1 节，身份规则见第 5.3 节。

worker 开关及连接器 Skills 更新复用 Agent 生命周期保护，状态保存于现有资源和 binding。运行中的 Codex、DSH 按第 6.3 节重启，直到新进程加载完成才报告生效；停止的 worker 保持停止，下次启动使用当前配置。revision 用于配置同步及加载验证。

### 6.3 worker 开关与 runtime 重启

worker 直接执行经过版本校验的官方 lark-cli。Runtime Adapter 在创建 Codex、DSH 进程时注入已生成的环境变量，并加载 Agent 技能目录。Agent Engine 在现有生命周期入口内组织连接器配置更新，复用 `lifecycle.Coordinator`、runtime 的 `Stop` / `Start`、Extension Driver 的准备与启用以及 Skills Controller 的目录操作。运行目录、会话记录和用户初始化设置由现有 runtime 继续管理。

1. 开启：保存 binding 开启意图，准备完整的 Skills 及 reference；目标运行环境将官方 CLI 所在目录加入 PATH，并设置 `LARKSUITE_CLI_CONFIG_DIR` 指向全局配置目录。
2. 关闭或尚未配置：目标环境将 `LARKSUITE_CLI_CONFIG_DIR` 明确设置为该 worker 运行目录内不含应用配置的专用目录，清除可能覆盖配置和身份的继承变量；按第 6.4 节清理受管理 Skills。此默认环境同样适用于从未创建飞书 binding 的 worker，防止自动读取主机的 `~/.lark-cli`。
3. 配置更新入口记录原运行意图，在 Agent 生命周期保护下暂停新任务并等待当前任务结束，调用 runtime 的 Stop，确认主进程和相关子进程结束后，启用目标 Extension projection、更新环境和 Skills。Extension reconciliation 使用 `DeferRuntimeReload`，启动由该入口统一安排。原来停止的 worker 只更新配置，保持停止。
4. 对原来运行中的 worker，复用 `reloadRuntimeLocked` 已有的停止状态观察、Start、运行状态保存和 Extension 加载验证步骤，使用更新后的环境恢复运行。已有运行目录和会话记录保留，本次配置更新不执行用户 InitShell 或重新配置基础凭证。
5. 等待任务结束、停止进程或确认子进程退出失败时，记录具体阶段和错误，保留重试入口，生效配置及 Skills 保持原状。旧进程结束后，配置准备、启用或启动失败时保持 runtime 停止；加载验证失败时终止本次新进程并记录错误。关闭操作确认旧进程结束且关闭环境已保存后生效，恢复运行状态须再通过启动检查。

Codex、DSH 的 Stop 在现有 runtime 进程管理代码中完成主进程、进程组及其相关子进程的清理。主进程自行退出或停止前已经退出时，仍检查其受管理进程组并清理剩余成员；完成退出验证后才返回停止成功。支持平台沿用各自的进程管理方式，错误和超时返回给上述配置更新入口。服务恢复时复用 runtime 已保存的进程信息，并核对进程身份后处理残留进程。

应用配置、个人授权和断开操作由全局连接器页面管理；worker 的正常任务按 Skills 使用官方业务命令。CLI 子进程继承当前 runtime 环境、工作目录及标准输入输出。

资源及 binding 是开关状态的持久化来源。服务启动先恢复未完成的管理操作，再由 Runtime Adapter 生成环境。global revision 与 binding.applied_revision 在配置应用和启动验证时核对；失败状态及恢复进度继续保存在现有管理记录中。

全局断开或修改共享配置时，`internal/apps` 按资源管理流程串行处理，并通过同一 Engine 配置更新入口停止全部受影响 runtime；确认主进程和相关 CLI 子进程结束后更新共享配置，再分别更新环境与 Skills，并按原运行意图启动。授权等待期间释放 Agent 生命周期锁，管理操作记录保留启动限制；自动恢复、手动启动和任务触发的启动均检查该记录，操作未完成时保持等待。处理完成后使用最新全局状态生成环境。

上述设置管理 CSGClaw 提供的连接器使用方式。host runtime 仍遵循同一操作系统账号的访问权限；拥有相应主机权限的任务可以显式指定其他 CLI 路径或配置。严格隔离需要操作系统权限或隔离环境支持。

### 6.4 Skills 安装与来源管理

安装经过版本锁定的官方 `lark-*` Skills，保留官方目录名称和 reference 相对路径。全局安装复用 `InstallArchiveWithOptions` 的校验逻辑，记录版本与内容摘要。

来源信息保存在现有状态文件的 `app_installations` 分区中，对应 worker 的 `binding.skills` 数组，每项包含 `skill_name`、`bundle_version`、`content_digest`、`managed`；通过所属 binding 的 `ResourceID` 关联全局连接器。复用 binding 持久化流程，不新增独立来源文件；官方 `SKILL.md` 保持原样。worker 开启连接器时，将全局 Skills 及 reference 完整复制到其技能目录，复制成功后保存对应来源记录；profile 根据这些记录展示来源。复制与来源记录更新通过第 6.3 节的 Engine 配置更新入口执行，在旧进程结束后修改 Agent 技能目录与来源记录；操作记录协调中断恢复，完成后按原运行意图启动并验证。个人安装的其他技能保持原样。

- 每个 worker 根据飞书 binding 的开启状态安装所需技能；重复开启复用内容一致的技能目录。
- Agent 技能目录已有同名、同版本且内容相同的个人技能时复用该目录，目录仍归用户管理。
- 同名内容冲突时显示 `skill_conflict`，保持原文件，待用户处理后重试。
- 更新或删除受管理目录前，核对当前内容与已安装摘要；发现用户修改时保留目录并显示冲突。
- worker 关闭连接器或解除绑定时，仅删除该 worker 中内容未变且归连接器管理的技能目录。用户原有技能禁用状态保留。
- Agent profile 展示连接器提供的技能及版本；版本更新只作用于本次连接器管理的目录。

## 7. 凭证、断开与配置独立性

### 7.1 凭证归属

新建 CLI 连接器的 App Secret、个人 access token、refresh token 和 DPoP 密钥均由官方 CLI 管理。CSGClaw 全局资源保存账号配置引用、脱敏身份、权限摘要和授权状态。已有 MCP 资源继续使用当前凭证存储方式。

官方 UAT 使用 `app_id + user_open_id` 作为存储键；`LARKSUITE_CLI_CONFIG_DIR` 只隔离配置。不同平台的凭证存储方式也有差异，不能依据配置目录不同就判定凭证完全独立。[Token 存储实现](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/internal/auth/token_store.go)、[Windows 凭证实现](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/internal/keychain/keychain_windows.go)

全局连接器记录继续保存在 `app_installations`，其 CLI 配置目录使用现有状态文件同级的 `apps/<resource_id>/lark-cli/`。全局资源保存目录引用、应用与账号标识及身份状态。官方 CLI 负责该目录中的配置文件以及原生凭证存储中的 App Secret、token 和 DPoP 密钥；凭证并非全部位于配置目录内。

全局连接器保存一个机器人的配置和对应个人授权。初始化、授权、状态检查及全部 worker 调用通过 `LARKSUITE_CLI_CONFIG_DIR` 使用同一稳定目录，共用官方刷新锁。Agent 目录仅保存配置引用、运行环境和 Skills 副本。[官方刷新锁实现](https://github.com/larksuite/cli/blob/9067ec079bfa0b1ae2266cd91d1c1ee4a1ce824d/internal/auth/token_lock.go)

配置目录路径保持稳定，更新文件采用原子替换。登录、退出、断开和重新配置复用资源级串行管理，修改共享配置前按第 6.3 节停止受影响的 runtime 及相关 CLI 子进程。授权等待期间不持有 Agent 生命周期锁，后端保留管理操作状态；配置完成后按原运行意图恢复 runtime。日常 token 刷新由官方 CLI 的同目录刷新锁协调。

全局机器人或个人账号更新前保存可恢复配置，验证成功后增加 revision，并更新已开启 worker 的环境和 Skills，再启动原来运行中的 runtime。启动失败的 worker 保持停止并提供修复入口，其他已验证的 worker 正常运行。配置验证失败时恢复原配置，核对身份及 revision 后按原运行意图启动；原配置无效时使用第 6.3 节的未配置环境。迁移与管理操作仅在修改共享配置期间互斥，等待配置、授权修复或首次启动时允许更新；后续同步读取最新 revision，提交前发生变化则重新准备。

### 7.2 管理操作

| 操作 | 处理结果 |
|-|-|
| worker profile 关闭飞书连接器 | 保存关闭意图，停止旧 runtime 及相关子进程，更新为未配置环境并清理受管理 Skills，重启原来运行中的 worker；其他 worker 保持原状 |
| 全局断开飞书连接 | 按本节流程停止全部受影响 runtime，清理生效配置，更新未配置环境和 Skills；保留 worker 开关意图，原来运行中的 worker 重新启动 |
| 删除 Agent | 删除绑定及 Agent 运行配置，保留全局资源 |
| 退出个人授权 | 显示全部已开启的 worker，执行官方 `auth logout` 并检查结果；机器人状态单独保留 |
| 修改应用或个人账号 | 全局配置验证成功后增加 revision，更新所有已开启 worker 的运行配置；旧任务不得写回 |
| 修改或删除飞书渠道 | 仅影响渠道；连接器已经保存的应用配置保持独立 |

worker 关闭仅改变自身 binding 和运行环境；全局断开影响全部使用该连接器的 worker。已经被飞书接受的业务请求可能完成，任务记录保留实际结果。断开保留官方账号授权；退出个人授权由明确的 `auth logout` 操作处理。

**全局断开飞书连接：**页面展示受影响 worker，后端持久化断开意图，使旧授权会话失效并结束相关管理进程；停止受影响的 Codex、DSH 及 CLI 子进程，确认结束后清理当前应用和账号的生效配置、完成标记与状态缓存，更新所有相关 worker 为未配置环境，并按第 6.4 节清理受管理 Skills。唯一资源记录、目录路径和 worker 开关意图保留；原来运行中的 worker 以未配置环境重新启动，原来停止的 worker 保持停止。官方凭证存储中的授权保留，重新配置时按目标身份验证。

断开进度复用现有资源管理记录。配置缺失、损坏或部分已清理时继续剩余步骤；停止失败显示处理中或失败，不能报告已断开；清理失败显示 `cleanup_pending`。旧进程结束后，尚未完成环境更新的 worker 保持停止。完成后显示待配置并提供重新配置入口；重新配置可继续剩余清理，成功后按 worker 开关及原运行意图恢复运行。

授权文件与配置目录使用私有权限；前端响应、SSE、错误消息、日志和 Skills 均不得包含长期凭证。浏览器地址限定为官方允许的 HTTPS 域名。新增管理接口沿用当前应用管理访问规则，并校验 Origin。

## 8. API 与页面状态

保留当前 `/api/v1/connectors/resources` 和 `/api/v1/agents/{id}/connectors` 资源、绑定接口。飞书资源创建在服务层保证全局唯一；worker 开启时由后端解析唯一资源并创建或启用 binding，关闭时复用现有 binding 启停操作。全局页面提供机器人配置，worker profile 的连接器页面提供飞书开关和生效状态。新增接口如下，名称为设计建议：

| 接口 | 作用与关键约束 |
|-|-|
| `POST /connectors/resources/{id}/auth-sessions` | 启动或恢复连接；请求包含阶段、operation_id、目标 resource_revision 及后端允许的配置目标字段，不接收前端拼装的执行命令；按第 5.5 节处理重复请求 |
| `GET /connectors/resources/{id}/auth-sessions/{session_id}` | 返回阶段、状态、授权地址、有效期和脱敏错误 |
| `DELETE /connectors/resources/{id}/auth-sessions/{session_id}` | 记录取消意图并阻止迟到结果提交；等待关联 CLI 进程结束并检查配置，完成前返回处理中 |
| `POST /connectors/resources/{id}/auth/logout` | 退出个人授权；响应包含受影响 worker 与最新身份状态 |
| `POST /connectors/resources/{id}/repair` | 按 operation_id 幂等重试安装、技能同步、清理和配置恢复；返回恢复阶段及待处理原因 |

上述路径均位于 `/api/v1` 下。现有 Agent OAuth start 接口解析唯一全局资源后进入同一授权会话逻辑；worker 页面通过全局设置入口进行授权。

CLI 连接器返回 `package_status`、`auth_status.bot`、`auth_status.user`、`skills_status`、`runtime_status`；现有 `Status` 用于展示综合状态。`Tools` 对 CLI 返回空集合，页面显示官方 Skills 和能力说明，不用工具数量判断是否连接成功。

区分三种完成结果：全局资源已授权、Agent 配置已生成、运行进程已加载。全局连接器首次配置的个人授权尚未完成时显示“机器人可用，个人账号待授权”；正常首次配置流程在个人授权完成后才进入完成状态。历史机器人配置迁移后显示“机器人可用”，个人身份显示“未配置”，不作为待完成授权任务提示用户。

页面状态由后端配置检查结果生成；缓存状态携带 revision 和检查时间，过期或与当前 revision 不符时显示待检查。普通页面加载读取缓存；用户主动点击“刷新状态”调用 `POST /connectors/resources/{id}/status-check`，合并同一 revision 的进行中检查并限制重复请求频率，重新读取配置、账号和阶段结果，按需验证凭证。检查结果提交前核对 revision，旧结果不得覆盖新配置。配置错误时更新为待修复，保留重新配置及断开连接入口；网络检查失败单独展示检查失败原因。

`POST /connectors/resources/{id}/disconnect` 执行第 7.2 节的全局断开连接，接收 operation_id 和 resource_revision，返回操作阶段、受影响 worker 和最新状态。重新配置由 auth-sessions 入口进入第 5.5 节流程，必要时继续未完成清理。repair、disconnect 和 auth-sessions 共用资源级管理操作记录及串行控制；同一操作可重复提交，其他目标在已有管理操作结束后执行。处理中返回现有操作及可轮询入口，revision 冲突返回最新状态供页面刷新。

修改文件主要为 `api/apps.ts`、`GlobalAppsPanel`、`AppSettingsDialog`、`AgentAppsPanel` 以及 Agent profile 的渠道页面。全局连接器页面负责 lark-cli 安装、应用设置、个人授权、状态检查和修复；worker profile 的连接器页面负责开启、关闭及展示该 worker 的生效状态。渠道页面移除 lark-cli 安装、初始化、清理与状态展示；后端同步移除渠道注册、更新、删除流程中的 CLI 调用。旧的 Agent lark-cli 管理接口停止从 Participant 创建配置，兼容入口返回连接器管理地址。界面继续遵循现有前端目录、国际化和部署子路径规则。

## 9. 自动迁移与故障恢复

### 9.1 迁移对象与映射

升级前扫描 CSGClaw 管理的 `feishu-lark-cli` Extension、CLI 配置以及飞书渠道关联，保存目标 worker 清单。升级前已配置飞书渠道或 lark-cli 的 worker 均属于迁移对象；迁移后为这些 worker 开启唯一全局飞书连接器。worker 原有运行或停止状态保持不变，迁移不自动启动已停止的 worker。

在尚未创建全局配置时，从能够读取并解析应用身份的历史 lark-cli 配置中随机选择一份，完整保留其机器人、个人账号、授权引用及身份设置。选择结果、源配置摘要、唯一目标资源 ID 和目标 worker 清单在执行迁移前持久化。随机选择只执行一次；重试和重启继续使用已记录的结果。已有全局配置时直接复用。没有可读取的历史 lark-cli 配置时，保留目标 worker 的开启意图，全局入口显示待配置，用户完成全局配置后同步生效。

全部目标 worker 的 binding 指向同一个 ResourceID，身份按第 5.3 节处理。其他历史 CLI 配置作为迁移恢复材料保存。

渠道 IM 的应用凭证、机器人、路由、启停配置及 worker 关联保持升级前状态。仅在解析被选中的旧 CLI 配置时一次性读取它引用的 Participant 凭证；迁移不向 Participant 写入全局机器人的设置。渠道与全局连接器使用不同机器人时，历史附件恢复指令仍遵循第 5.3 节的应用一致性检查。

迁移器按第 7.1 节的存储规则，通过官方 CLI 配置入口迁移所选配置，凭证经标准输入或私有文件传递，并检查 token 与 DPoP 密钥引用可访问。全局配置已建立后，迁移重试复用当前配置，保留用户后续更新。迁移前停止涉及同一凭证的旧 runtime 及 CLI 子进程，在相关 worker 完成目录切换前暂缓启动，防止新旧目录使用不同刷新锁修改同一凭证。

### 9.2 可恢复的执行顺序

启动时在 Agent 自动恢复、Extension reconciliation 和运行配置清理之前加载迁移记录，包括全局选择结果、目标 worker 及其操作阶段，并登记迁移兼容 Source。启动前完成配置准备或恢复原配置，不等待 `RuntimeLoaded`；随后按各个 worker 原运行状态恢复，启动后检查加载结果。渠道 IM 沿用原有恢复流程与配置。

`internal/apps` 负责迁移记录和恢复编排，通过第 6.3 节的 Engine 配置更新入口执行停止、配置启用、Skills 更新、启动与验证。迁移操作另外提供历史配置恢复材料和提交阶段，入口核对 Agent、Extension、原配置与目标全局资源；恢复材料保存在普通 generation 清理范围之外。binding 与 Source 继续使用现有机制，历史配置的恢复及清理按下述阶段执行。

Source 在准备阶段读取历史配置；进入切换前，持久化目标全局资源、revision、目标 binding 和操作阶段。激活及运行验证阶段解析目标全局配置，使 `PrepareRuntime` 和 reconciliation 始终使用同一资源及 revision；恢复阶段解析记录中的原配置。正常运行及迁移完成后，Source 读取 worker binding 引用的唯一全局资源。重启恢复采用相同规则，阶段确认前暂缓受影响 runtime 启动。

原来仅配置飞书渠道的 worker 在全局配置准备完成后创建 Extension；准备阶段保持连接器待配置，失败时保留开启意图并显示待修复状态。其渠道 IM 持续使用原配置。

1. 保存一次性的全局选择结果及迁移目标清单。每个 worker 使用由 Agent ID 和 Extension 名称确定的稳定 `migration_key`，记录原运行状态、Source、执行路径、身份设置和技能目录恢复信息。
2. 创建或复用唯一全局资源，准备所选 CLI 配置和官方 Skills；准备各个目标 worker 的配置引用和技能副本。候选 binding 引用同一个 ResourceID，待配置生效后提交开启状态。保留官方授权存储中的现有有效凭证。
3. 检查所选配置的 App ID、账号、身份设置、CLI 和 Skills，验证凭证可访问性；授权过期如实展示。迁移准备失败时保留原配置及错误状态，重试仍使用已记录的选择结果。
4. 在各个 Agent 生命周期保护下停止旧 runtime 及相关子进程，更新环境和连接器 Skills；确认同一凭证涉及的旧 runtime 和 CLI 子进程全部结束、目录切换完成后，按原运行意图启动 Codex、DSH，验证实际加载的全局资源及 revision 后提交 binding 和 applied_revision。提交 binding 前切换或加载失败时，按操作记录恢复该 worker 原运行配置和本次修改的技能，保留重试入口。
5. 原来停止的 worker 保持停止，完成配置检查后提交开启的 binding，并记录 `runtime_verification=pending`。下次启动时读取全局配置最新 revision 并验证；失败时保留已提交的全局引用，保持 runtime 停止并重试当前配置。
6. 同一凭证涉及的旧 runtime 和 CLI 子进程全部结束、运行配置完成目录切换后，才允许相关 runtime 使用全局配置启动。中断恢复继续检查此条件。全部对应配置通过检查后清理恢复材料；待首次运行验证的记录继续保留材料。清理只处理 CLI 迁移文件及本次管理的 Skills，保留渠道 IM 配置、全局凭证及其他仍在使用的文件。

迁移记录逐步保存资源写入、worker 配置、技能更新和恢复进度。`runtime_adapter.go` 以 binding 提交为界：提交前失败按操作记录恢复旧 projection，提交后失败保留全局引用并重试最新配置。兼容 Source 仅服务尚未提交 binding 的历史记录，读取保存的配置与凭证引用；恢复进度在重启后继续执行。连接器配置更新统一遵循第 6.3 节的停止及启动顺序；普通 Extension 的 Source 失效处理与 generation 清理规则保持原有职责，迁移恢复材料按本节管理。

迁移状态分别展示全局配置结果与各个 worker 的结果。失败显示 `migration_failed`，清理失败显示 `cleanup_pending`，重试复用原资源 ID 和选择结果。授权存储保留最近一次有效刷新结果，恢复时不使用旧 token 副本覆盖已更新的凭证。迁移尚未完成的 worker 关闭连接器时，记录其最新开关意图，后续重试保留该选择。

同一凭证仍被旧执行入口引用时，全局资源及受影响 worker 显示 `waiting_for_legacy_callers`。页面列出阻塞 worker、旧执行入口、最近失败阶段及具体错误，并提供重试迁移或关闭对应旧连接器入口的操作。关闭操作必须确认旧进程结束且关闭环境已保存，才能解除等待；关闭意图在后续迁移重试中保留。迁移成功但仍等待其他 worker 的页面分别展示“配置已完成”和当前等待原因。每次修复后重新检查全部相关旧入口，仅在第 9.2 节启动条件满足时恢复 runtime。

## 10. 故障恢复验收

以下用例覆盖全局连接器、worker binding、runtime 环境和受管理文件的最终状态。每次中断后均需验证重复请求及服务重启能够继续恢复，资源和 binding 保持唯一，用户原有文件及渠道配置保持完整。

| 故障场景 | 操作与预期结果 |
|-|-|
| 未配置但已有失败残留 | 分别保留临时安装目录、损坏配置、失效会话和部分 worker 配置后，刷新状态并重复重新配置。状态检查报告当前问题；重新配置复用 ResourceID，恢复未完成阶段，最终应用和账号符合本次选择，已开启 worker 引用最新 revision，验证通过后启动使用新环境的 runtime；临时文件完成清理，个人文件保留。 |
| 错误配置显示已配置 | 注入错误 App ID、账号引用或完成缓存，执行全局断开连接并重新配置。断开期间停止全部受影响 runtime 及相关 CLI 子进程，完成后显示待配置；重新配置采用本次应用和账号，旧 completed 状态及旧缓存不能恢复错误配置。worker 开关意图保留，运行验证通过后恢复 runtime。 |
| 重复点击与并发请求 | 对相同 operation_id 重复发送配置、断开或修复请求，只产生一份操作结果；相同 operation_id 携带不同目标返回冲突。不同目标按资源管理规则串行处理，旧 revision 请求不能覆盖新配置，资源、binding、安装目录及授权任务不重复创建。 |
| 断开或清理中途失败 | 在配置引用清除、运行文件清理和状态保存之间分别中断。恢复后显示 cleanup_pending 并保持 runtime 停止或使用未配置环境；重复断开或重新配置能够继续剩余清理，最终进入待配置或完成新配置。配置缺失及损坏均可处理，独立授权存储和用户修改过的 Skills 保留。 |
| 授权成功后服务立即重启 | 在 CLI 保存授权后、CSGClaw 提交结果前重启。恢复任务核对已保存应用、账号和凭证，复用已完成阶段，继续生成配置；binding 保持唯一，worker 加载最新 revision 后恢复运行，有效凭证保持可用。 |
| 取消授权与 CLI 写入同时发生 | 在 CLI 写入配置或凭证时取消会话并发起重新配置。必须等待旧进程结束并核对配置后才执行新目标；旧结果不能改变新应用与账号，恢复完成前 runtime 保持停止或使用未配置环境，凭证按账号归属保留。 |
| 业务执行期间切换账号 | 在 worker 正在调用 lark-cli 时修改全局账号。停止受影响 Codex、DSH 及其 CLI 子进程，确认结束后修改共享配置；停止失败时不继续修改并展示错误。新账号验证完成后，按原运行意图启动 worker，环境及 revision 使用新配置。 |
| 普通配置更新中途失败 | 分别在 Extension 准备、启用、Skills 更新、runtime 启动和加载验证阶段注入失败。确认全部生效文件修改发生在旧进程结束之后；停止后发生失败时保持 runtime 停止，重复操作继续恢复；原来停止的 worker 保持停止，原运行意图、会话记录及用户初始化设置保留。 |
| 主进程退出后子进程仍运行 | 在 Codex、DSH 各自支持的平台，让主进程正常退出或在调用 Stop 前退出，同时保留一个相关 CLI 子进程。Stop 必须清理剩余受管理进程并验证结束；清理失败时配置更新保持等待或失败，不能继续修改共享配置或报告断开成功。 |
| 部分 worker 迁移失败 | 使一个 worker 配置切换失败并恢复旧引用。相关 worker 显示 waiting_for_legacy_callers，列出阻塞对象和错误原因；重试迁移或关闭旧入口后重新检查 runtime 启动条件。绑定最终指向唯一全局资源，渠道 IM 配置和最近有效凭证保留，恢复材料按验证结果清理。 |
| worker 未开启且主机已有 CLI 配置 | 主机 PATH 中安装 lark-cli 且主机默认目录已有有效配置；未开启或从未绑定的 worker 启动后，其 LARKSUITE_CLI_CONFIG_DIR 指向不含应用配置的专用目录，普通 lark-cli 调用显示未配置。开启后重启使用全局目录，关闭后重启恢复未配置环境。 |
| 停止中的 worker 首次启动失败 | 迁移期间保持 worker 停止，首次启动加载失败后保留已提交 binding 和全局引用，保持 runtime 停止并显示修复入口。重试使用最新全局 revision；验证通过后启动使用新环境的 runtime 并清理已完成的恢复材料。 |
| 用户修改受管理 Skills 后关闭连接器 | 关闭 binding，确认旧 runtime 及相关子进程结束，再以未配置环境重启原来运行中的 worker；用户修改的 Skills 保留并展示冲突，全局账号和其他 worker 保持原状。重新开启时处理冲突并使用全局配置重启。 |

## 11. 权限标识符清单

以下为 2026-10-11 对应代码中的指定权限清单，tenant 共 119 项，user 共 133 项。个人登录另外由官方 CLI 添加 offline_access。更新代码权限策略时，同步更新此清单。

```json
{
  "tenant": [
    "application:application:self_manage",
    "base:app:copy",
    "base:app:create",
    "base:app:read",
    "base:app:update",
    "base:dashboard:create",
    "base:dashboard:delete",
    "base:dashboard:read",
    "base:dashboard:update",
    "base:field:create",
    "base:field:delete",
    "base:field:read",
    "base:field:update",
    "base:form:create",
    "base:form:delete",
    "base:form:read",
    "base:form:update",
    "base:history:read",
    "base:record:create",
    "base:record:delete",
    "base:record:read",
    "base:record:update",
    "base:role:create",
    "base:role:delete",
    "base:role:read",
    "base:role:update",
    "base:table:create",
    "base:table:delete",
    "base:table:read",
    "base:table:update",
    "base:view:read",
    "base:view:write_only",
    "base:workflow:create",
    "base:workflow:read",
    "base:workflow:update",
    "board:whiteboard:node:create",
    "board:whiteboard:node:read",
    "contact:contact.base:readonly",
    "contact:user.base:readonly",
    "docs:document.comment:create",
    "docs:document.comment:delete",
    "docs:document.comment:read",
    "docs:document.comment:update",
    "docs:document.comment:write_only",
    "docs:document.content:read",
    "docs:document.media:download",
    "docs:document.media:upload",
    "docs:document:copy",
    "docs:document:export",
    "docs:document:import",
    "docs:permission.member:auth",
    "docs:permission.member:create",
    "docs:permission.member:delete",
    "docs:permission.member:retrieve",
    "docs:permission.member:transfer",
    "docs:permission.setting:read",
    "docs:permission.setting:write_only",
    "docx:document:create",
    "docx:document:readonly",
    "docx:document:write_only",
    "drive:drive.metadata:readonly",
    "drive:file:download",
    "drive:file:upload",
    "drive:file:view_record:readonly",
    "im:chat.managers:write_only",
    "im:chat.members:read",
    "im:chat.members:write_only",
    "im:chat.moderation:read",
    "im:chat:create",
    "im:chat:moderation:write_only",
    "im:chat:read",
    "im:chat:update",
    "im:message",
    "im:message.group_msg",
    "im:message.p2p_msg:readonly",
    "im:message.pins:read",
    "im:message.pins:write_only",
    "im:message.reactions:read",
    "im:message.reactions:write_only",
    "im:message:readonly",
    "im:message:send_as_bot",
    "im:message:update",
    "im:resource",
    "minutes:minutes.artifacts:read",
    "minutes:minutes.search:read",
    "sheets:spreadsheet.meta:read",
    "sheets:spreadsheet.meta:write_only",
    "sheets:spreadsheet:create",
    "sheets:spreadsheet:read",
    "sheets:spreadsheet:write_only",
    "slides:presentation:create",
    "slides:presentation:read",
    "slides:presentation:update",
    "slides:presentation:write_only",
    "space:document:delete",
    "space:document:move",
    "space:document:shortcut",
    "space:folder:create",
    "task:attachment:write",
    "task:comment:write",
    "task:custom_field:read",
    "task:custom_field:write",
    "task:section:read",
    "task:section:write",
    "task:task:read",
    "task:tasklist:read",
    "vc:meeting.search:read",
    "vc:note:read",
    "wiki:member:create",
    "wiki:member:retrieve",
    "wiki:member:update",
    "wiki:node:copy",
    "wiki:node:create",
    "wiki:node:move",
    "wiki:node:read",
    "wiki:node:retrieve",
    "wiki:space:read",
    "wiki:space:retrieve",
    "wiki:space:write_only"
  ],
  "user": [
    "approval:instance:read",
    "approval:instance:write",
    "approval:task:read",
    "approval:task:write",
    "base:app:copy",
    "base:app:create",
    "base:app:read",
    "base:app:update",
    "base:dashboard:create",
    "base:dashboard:delete",
    "base:dashboard:read",
    "base:dashboard:update",
    "base:field:create",
    "base:field:delete",
    "base:field:read",
    "base:field:update",
    "base:form:create",
    "base:form:delete",
    "base:form:read",
    "base:form:update",
    "base:history:read",
    "base:record:create",
    "base:record:delete",
    "base:record:read",
    "base:record:update",
    "base:role:create",
    "base:role:delete",
    "base:role:read",
    "base:role:update",
    "base:table:create",
    "base:table:delete",
    "base:table:read",
    "base:table:update",
    "base:view:read",
    "base:view:write_only",
    "base:workflow:create",
    "base:workflow:read",
    "base:workflow:update",
    "board:whiteboard:node:create",
    "board:whiteboard:node:read",
    "contact:user:search",
    "docs:document.comment:create",
    "docs:document.comment:delete",
    "docs:document.comment:read",
    "docs:document.comment:update",
    "docs:document.comment:write_only",
    "docs:document.content:read",
    "docs:document.media:download",
    "docs:document.media:upload",
    "docs:document:copy",
    "docs:document:export",
    "docs:document:import",
    "docs:permission.member:apply",
    "docs:permission.member:auth",
    "docs:permission.member:create",
    "docs:permission.member:delete",
    "docs:permission.member:retrieve",
    "docs:permission.member:transfer",
    "docs:permission.setting:read",
    "docs:permission.setting:write_only",
    "docs:secure_label:write_only",
    "docx:document:create",
    "docx:document:readonly",
    "docx:document:write_only",
    "drive:drive.metadata:readonly",
    "drive:file:download",
    "drive:file:upload",
    "drive:file:view_record:readonly",
    "drive:quota_detail:read_one",
    "im:chat.managers:write_only",
    "im:chat.members:read",
    "im:chat.members:write_only",
    "im:chat.moderation:read",
    "im:chat.nickname:read",
    "im:chat.nickname:write",
    "im:chat.user_setting:read",
    "im:chat.user_setting:write",
    "im:chat:moderation:write_only",
    "im:chat:read",
    "im:chat:update",
    "im:feed.flag:read",
    "im:feed.flag:write",
    "im:message",
    "im:message.group_msg:get_as_user",
    "im:message.p2p_msg:get_as_user",
    "im:message.pins:read",
    "im:message.pins:write_only",
    "im:message.reactions:read",
    "im:message.reactions:write_only",
    "im:message:readonly",
    "im:message:update",
    "im:resource",
    "mail:user_mailbox.message:modify",
    "mail:user_mailbox:readonly",
    "minutes:minutes.artifacts:read",
    "minutes:minutes.search:read",
    "minutes:minutes.upload:write",
    "sheets:spreadsheet.meta:read",
    "sheets:spreadsheet.meta:write_only",
    "sheets:spreadsheet:create",
    "sheets:spreadsheet:read",
    "sheets:spreadsheet:write_only",
    "slides:presentation:create",
    "slides:presentation:read",
    "slides:presentation:update",
    "slides:presentation:write_only",
    "space:document:delete",
    "space:document:move",
    "space:document:shortcut",
    "space:folder:create",
    "spark:app:read",
    "spark:app:write",
    "task:attachment:write",
    "task:comment:write",
    "task:custom_field:read",
    "task:custom_field:write",
    "task:section:read",
    "task:section:write",
    "task:task:read",
    "task:tasklist:read",
    "vc:meeting.search:read",
    "vc:note:read",
    "wiki:member:create",
    "wiki:member:retrieve",
    "wiki:member:update",
    "wiki:node:copy",
    "wiki:node:create",
    "wiki:node:move",
    "wiki:node:read",
    "wiki:node:retrieve",
    "wiki:space:read",
    "wiki:space:retrieve",
    "wiki:space:write_only"
  ]
}
```
