# sub2api 项目开发指南

> 本文档记录项目环境配置、常见坑点和注意事项，供 Claude Code 和团队成员参考。

## 一、项目基本信息

| 项目 | 说明 |
|------|------|
| **上游仓库** | Wei-Shaw/sub2api |
| **Fork 仓库** | bayma888/sub2api-bmai |
| **技术栈** | Go 后端 (Ent ORM + Gin) + Vue3 前端 (pnpm) |
| **数据库** | PostgreSQL 16 + Redis |
| **包管理** | 后端: go modules, 前端: **pnpm**（不是 npm） |

## 二、本地环境配置

### PostgreSQL 16 (Windows 服务)

| 配置项 | 值 |
|--------|-----|
| 端口 | 5432 |
| psql 路径 | `C:\Program Files\PostgreSQL\16\bin\psql.exe` |
| pg_hba.conf | `C:\Program Files\PostgreSQL\16\data\pg_hba.conf` |
| 数据库凭据 | user=`sub2api`, password=`sub2api`, dbname=`sub2api` |
| 超级用户 | user=`postgres`, password=`postgres` |

### Redis

| 配置项 | 值 |
|--------|-----|
| 端口 | 6379 |
| 密码 | 无 |

### 开发工具

```bash
# golangci-lint（CI 用 v2.13，本地建议装同一版以免版本差异带来的噪音）
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13

# pnpm (前端包管理)
npm install -g pnpm
```

## 三、CI/CD 流水线

### GitHub Actions Workflows

| Workflow | 触发条件 | 检查内容 |
|----------|----------|----------|
| **backend-ci.yml** | push, pull_request | 单元测试 + 集成测试 + golangci-lint v2.13 |
| **security-scan.yml** | push, pull_request, 每周一 | govulncheck + gosec + pnpm audit |
| **release.yml** | tag `v*` | 构建发布（PR 不触发） |

### CI 要求

- Go 版本必须是 **1.27.0**：三个 workflow 都用 `go-version-file: backend/go.mod` 取版本，随后硬断言 `go version | grep -q 'go1.27.0'`。升级 Go 时要同时改 `backend/go.mod`、`backend-ci.yml`（两处）、`release.yml`、`security-scan.yml` 里的这句断言，**以及三个 Dockerfile 里的 Go 构建镜像**（`Dockerfile` / `deploy/Dockerfile` 的 `ARG GOLANG_IMAGE`、`backend/Dockerfile` 的 `FROM golang:`）。前者漏了 CI 会在版本校验步骤直接失败；**后者漏了 CI 不会报，而是等到有人用这些 Dockerfile 构建时才失败**（`go.mod requires go >= X (running Y; GOTOOLCHAIN=local)`）。
- 前端使用 `pnpm install --frozen-lockfile`，必须提交 `pnpm-lock.yaml`

### 本地测试命令

```bash
# 后端单元测试
cd backend && go test -tags=unit ./...

# 后端集成测试
cd backend && go test -tags=integration ./...

# 代码质量检查
cd backend && golangci-lint run ./...

# 前端依赖安装（必须用 pnpm）
cd frontend && pnpm install
```

## 四、常见坑点 & 解决方案

### 坑 1：pnpm-lock.yaml 必须同步提交

**问题**：`package.json` 新增依赖后，CI 的 `pnpm install --frozen-lockfile` 失败。

**原因**：上游 CI 使用 pnpm，lock 文件不同步会报错。

**解决**：
```bash
cd frontend
pnpm install  # 更新 pnpm-lock.yaml
git add pnpm-lock.yaml
git commit -m "chore: update pnpm-lock.yaml"
```

---

### 坑 2：npm 和 pnpm 的 node_modules 冲突

**问题**：之前用 npm 装过 `node_modules`，pnpm install 报 `EPERM` 错误。

**解决**：
```bash
cd frontend
rm -rf node_modules  # 或 PowerShell: Remove-Item -Recurse -Force node_modules
pnpm install
```

---

### 坑 3：PowerShell 中 bcrypt hash 的 `$` 被转义

**问题**：bcrypt hash 格式如 `$2a$10$xxx...`，PowerShell 把 `$2a` 当变量解析，导致数据丢失。

**解决**：将 SQL 写入文件，用 `psql -f` 执行：
```bash
# 错误示范（PowerShell 会吃掉 $）
psql -c "INSERT INTO users ... VALUES ('$2a$10$...')"

# 正确做法
echo "INSERT INTO users ... VALUES ('\$2a\$10\$...')" > temp.sql
psql -U sub2api -h 127.0.0.1 -d sub2api -f temp.sql
```

---

### 坑 4：psql 不支持中文路径

**问题**：`psql -f "D:\中文路径\file.sql"` 报错找不到文件。

**解决**：复制到纯英文路径再执行：
```bash
cp "D:\中文路径\file.sql" "C:\temp.sql"
psql -f "C:\temp.sql"
```

---

### 坑 5：PostgreSQL 密码重置流程

**场景**：忘记 PostgreSQL 密码。

**步骤**：
1. 修改 `C:\Program Files\PostgreSQL\16\data\pg_hba.conf`
   ```
   # 将 scram-sha-256 改为 trust
   host    all    all    127.0.0.1/32    trust
   ```
2. 重启 PostgreSQL 服务
   ```powershell
   Restart-Service postgresql-x64-16
   ```
3. 无密码登录并重置
   ```bash
   psql -U postgres -h 127.0.0.1
   ALTER USER sub2api WITH PASSWORD 'sub2api';
   ALTER USER postgres WITH PASSWORD 'postgres';
   ```
4. 改回 `scram-sha-256` 并重启

---

### 坑 6：Go interface 新增方法后 test stub 必须补全

**问题**：给 interface 新增方法后，编译报错 `does not implement interface (missing method XXX)`。

**原因**：所有测试文件中实现该 interface 的 stub/mock 都必须补上新方法。

**解决**：
```bash
# 搜索所有实现该 interface 的 struct
cd backend
grep -r "type.*Stub.*struct" internal/
grep -r "type.*Mock.*struct" internal/

# 逐一补全新方法
```

---

### 坑 7：Windows 上 psql 连 localhost 的 IPv6 问题

**问题**：psql 连 `localhost` 先尝试 IPv6 (::1)，可能报错后再回退 IPv4。

**建议**：直接用 `127.0.0.1` 代替 `localhost`。

---

### 坑 8：Windows 没有 make 命令

**问题**：CI 里用 `make test-unit`，本地 Windows 没有 make。

**解决**：直接用 Makefile 里的原始命令：
```bash
# 代替 make test-unit
go test -tags=unit ./...

# 代替 make test-integration
go test -tags=integration ./...
```

---

### 坑 9：Ent Schema 修改后必须重新生成

**问题**：修改 `ent/schema/*.go` 后，代码不生效。

**解决**：
```bash
cd backend
go generate ./ent  # 重新生成 ent 代码（json.RawMessage 字段会生成为同类型的 jsontext.Value，属预期）
git add ent/       # 生成的文件也要提交
```

---

### 坑 10：前端测试看似正常，但后端调用失败（模型映射被批量误改）

**典型现象**：
- 前端按钮点测看起来正常；
- 实际通过 API/客户端调用时返回 `Service temporarily unavailable` 或提示无可用账号；
- 常见于 OpenAI 账号（例如 Codex 模型）在批量修改后突然不可用。

**根因**：
- OpenAI 账号编辑页默认不显式展示映射规则，容易让人误以为“没映射也没关系”；
- 但在**批量修改同时选中不同平台账号**（OpenAI + Antigravity/Gemini）时，模型白名单/映射可能被跨平台策略覆盖；
- 结果是 OpenAI 账号的关键模型映射丢失或被改坏，后端选不到可用账号。

**修复方案（按优先级）**：
1. **快速修复（推荐）**：在批量修改中补回正确的透传映射（例如 `gpt-5.3-codex -> gpt-5.3-codex-spark`）。
2. **彻底重建**：删除并重新添加全部相关账号（最稳但成本高）。

**关键经验**：
- 如果某模型已被软件内置默认映射覆盖，通常不需要额外再加透传；
- 但当上游模型更新快于本仓库默认映射时，**手动批量添加透传映射**是最简单、最低风险的临时兜底方案；
- 批量操作前尽量按平台分组，不要混选不同平台账号。

---

### 坑 11：Windows 本地 `go run` 后端进入 setup 向导而不是主服务

**典型现象**：
```bash
cd backend && go run ./cmd/server/
# 日志：First run detected, starting setup wizard...
# 服务只监听 /setup/status、/setup/install 等向导路由，API 全部 404
```
更糟的是：如果此时已有实例占用 8080，第二个进程会直接 `bind: Only one usage of each socket address` 退出，看起来像「端口冲突」，实际根因是走错了模式。

**根因**：
- `internal/setup.GetDataDir()` 的优先级是 `DATA_DIR` 环境变量 > `/app/data`（存在且可写）> 当前目录；
- Windows 上 Go 把 `/app/data` 解析成 **`C:\app\data`**。这台机器上 `C:\app\data`（Docker/历史运行留下的，含 `logs/`）存在且可写，于是 `GetDataDir()` 返回 `/app/data`；
- `NeedsSetup()` 只检查 `GetDataDir()/config.yaml` 与 `GetDataDir()/.installed`。而开发用配置在 `backend/config.yaml`，于是被判成「首次安装」。
- 注意主服务的 viper 加载是**多路径搜索**（`/app/data` → `.` → `./config` → `/etc/sub2api`），找不到 `/app/data/config.yaml` 会回落到 `./config.yaml`，所以只有 `NeedsSetup()` 这一处会误判，这也是它看起来「时好时坏」的原因。

**解决**：显式指定 `DATA_DIR` 指向 `backend` 目录（推荐，同时让配置加载也走这里）：
```bash
cd backend
DATA_DIR="C:/Users/Administrator/Desktop/sub2api/backend" go run ./cmd/server/
```

或跳过向导判定：
```bash
cd backend && SKIP_SETUP=true go run ./cmd/server/
```

**排查口诀**：`curl http://127.0.0.1:8080/health` 返回 `{"status":"ok"}` 才是主服务；若只有 `/setup/*` 路由或 `/api/v1/*` 全 404，就是落进了 setup 模式。查占用：`netstat -ano | findstr :8080` + 任务管理器看 PID 是不是 `go-build\...\server.exe`。

> 提醒：`go run` 无热重载，改完 Go 代码必须重启后端；前端 Vite 才有 HMR。

---

### 坑 12：PR 提交前检查清单

提交 PR 前务必本地验证：

- [ ] `go test -tags=unit ./...` 通过
- [ ] `go test -tags=integration ./...` 通过
- [ ] `golangci-lint run ./...` 无新增问题
- [ ] `pnpm-lock.yaml` 已同步（如果改了 package.json）
- [ ] 所有 test stub 补全新接口方法（如果改了 interface）
- [ ] Ent 生成的代码已提交（如果改了 schema）

## 五、常用命令速查

### 数据库操作

```bash
# 连接数据库
psql -U sub2api -h 127.0.0.1 -d sub2api

# 查看所有用户
psql -U postgres -h 127.0.0.1 -c "\du"

# 查看所有数据库
psql -U postgres -h 127.0.0.1 -c "\l"

# 执行 SQL 文件
psql -U sub2api -h 127.0.0.1 -d sub2api -f migration.sql
```

### Git 操作

```bash
# 同步上游
git fetch upstream
git checkout main
git merge upstream/main
git push origin main

# 创建功能分支
git checkout -b feature/xxx

# Rebase 到最新 main
git fetch upstream
git rebase upstream/main
```

### 前端操作

```bash
# 安装依赖（必须用 pnpm）
cd frontend
pnpm install

# 开发服务器
pnpm dev

# 构建
pnpm build
```

### 后端操作

```bash
# 运行服务器（Windows 必须带 DATA_DIR，否则误判为首次安装 → setup 向导，见「坑 11」）
cd backend
DATA_DIR="C:/Users/Administrator/Desktop/sub2api/backend" go run ./cmd/server/

# 启动前确认 8080 未被旧实例占用
netstat -ano | grep ":8080" || echo "8080 free"
# 启动后验证（返回 {"status":"ok"} 说明是主服务）
curl -s http://127.0.0.1:8080/health

# 生成 Ent 代码
go generate ./ent

# 运行测试
go test -tags=unit ./...
go test -tags=integration ./...

# Lint 检查
golangci-lint run ./...
```

## 六、项目结构速览

```
sub2api-bmai/
├── backend/
│   ├── cmd/server/          # 主程序入口
│   ├── ent/                 # Ent ORM 生成代码
│   │   └── schema/          # 数据库 Schema 定义
│   ├── internal/
│   │   ├── handler/         # HTTP 处理器
│   │   ├── service/         # 业务逻辑
│   │   ├── repository/      # 数据访问层
│   │   └── server/          # 服务器配置
│   ├── migrations/          # 数据库迁移脚本
│   └── config.yaml          # 配置文件
├── frontend/
│   ├── src/
│   │   ├── api/             # API 调用
│   │   ├── components/      # Vue 组件
│   │   ├── views/           # 页面视图
│   │   ├── types/           # TypeScript 类型
│   │   └── i18n/            # 国际化
│   ├── package.json         # 依赖配置
│   └── pnpm-lock.yaml       # pnpm 锁文件（必须提交）
└── .claude/
    └── CLAUDE.md            # 本文档
```

## 六·五、椒图（Jiaotu）原生平台

> 逐字段的协议契约与移植风险在 `docs/JIAOTU_NATIVE_INTEGRATION.md`。
> ⚠️ 该文件位于 `.gitignore` 的 `docs/*` 规则之下，**默认不进版本控制**；需要共享时执行
> `git add -f docs/JIAOTU_NATIVE_INTEGRATION.md`。本节只放日常开发必须知道的要点。

### 是什么

sub2api 的原生 `platform=jiaotu` 上游：**图片与视频都走 `POST /api/v1/ai/imageChat`（SSE）**，
直连 `https://api.jiaotuai.cn`，**不经过 kuikui 的本地桥接服务**。一个 sub2api 账号 = 一个椒图号。
入站保持 OpenAI 协议：图片 `/v1/images/generations|edits`，视频 `/v1/videos`。

### 本地启动

后端必须带 `DATA_DIR`（见「坑 11」），否则会被误判成首次安装而进 setup 向导：

```bash
cd backend
DATA_DIR="C:/Users/Administrator/Desktop/sub2api/backend" go run ./cmd/server/
curl -s http://127.0.0.1:8080/health          # {"status":"ok"} 才是主服务
```

### 号池导入 / 维护

```bash
# 批量导入（粘贴 kuikui 的 .jiaotu-pool.json、{code,data} 状态包、裸数组，或纯文本 token 行）
curl -sX POST http://127.0.0.1:8080/api/v1/admin/accounts/import/jiaotu-pool \
  -H "Authorization: Bearer $ADMIN_JWT" -H 'Content-Type: application/json' \
  --data-binary @pool-body.json
# 幂等：按「号池 ID + token」双键去重；update_existing=false 时全部跳过

# 全池积分刷新（只打免费的 userBilling/page，不消耗积分）
curl -sX POST http://127.0.0.1:8080/api/v1/admin/accounts/jiaotu/maintenance \
  -H "Authorization: Bearer $ADMIN_JWT" -H 'Content-Type: application/json' \
  -d '{"refresh_points":true,"concurrency":5}'
```

- 分组需 `platform=jiaotu` 且 `allow_image_generation=true`（该默认值已对 jiaotu 自动开启）。
- 单号默认 `concurrency=1`：椒图按号计积分，并发打高容易触发风控。
- 管理端返回 `credentials` 原文（现有「管理员备份」语义），因此**日志与前端展示一律走
  `JiaotuIdentityForLog()` / `MaskJiaotuPhone()`**，不得输出 token 或完整手机号。

### 关键配置（`backend/config.yaml`）

```yaml
jiaotu:
  api_base: https://api.jiaotuai.cn
  models_cache_ttl_seconds: 600
  max_attempts: 3          # 单请求最多换号次数（= 最多尝试 4 个号）
  max_reference_mb: 10
  auto_answer_questions: true   # 见下方「上游会反问」
  sign_in_enabled: false       # 维护请求未传 sign_in 时，是否顺带做每日免费签到
  auto_register:
    enabled: false         # 真实注册会花接码费并可能触发风控，默认关闭
    pool_target: 0
    sms: { haozhu: {...}, my531: {...} }   # 凭据只放本地，绝不提交
```

`jiaotu.auto_register.enabled=false` 时注册入口**直接返回错误且不发任何网络请求**，
且它没有挂载到任何 HTTP 路由；图片/视频失败只做换号 + 冷却，绝不会自动去注册新号。

### 四个必须知道的坑

1. **上游会把 `imageChat` 当对话用**：提示词被判定不明确时不回图，而是回
   `text×N + questions×1 + end`。已实现「用原始需求自动作答一轮 + **保持同一 sessionId** 续发」，
   由 `jiaotu.auto_answer_questions` 控制（默认开）。关掉后椒图分组几乎只能靠碰运气出图。
2. **不要拿 `ImageTaskService.Complete()` 存非图片载荷**：它会按 OpenAI 图片响应校验
   （期望 `{"data":[{"url"|"b64_json"}]}`），视频载荷会被判成非法结果并改写成
   `upstream returned a non-JSON or invalid image response`。**这个错看起来像上游失败，实际生成已成功**，
   结果是积分已扣、产物 URL 被丢弃（真机烧掉过约 150 积分）。视频因此使用独立的
   `JiaotuVideoTaskService`（`vidtask_` 前缀、自有状态词 `in_progress/completed/failed`）。
3. **两套请求指纹不同**：JSON 接口（模型列表/上传/积分）带完整 `Sec-CH-UA*`/`Sec-Fetch-*`，
   而 `imageChat` 只能带 6 个头（与浏览器实际发出的一致，对齐 kuikui）。混发会被上游判为异常客户端。
4. **新平台不等于 user × platform 配额平台**：`user_platform_quotas.platform` 有 DB CHECK 约束，
   枚举与 `service.AllowedQuotaPlatforms` / `ent/schema/user_platform_quota.go` 三处同源。
   椒图不在这份白名单里（admin 也无法给它设限额），但 `HasUserPlatformQuotaLimit` 在 cache miss 时
   **fail-safe 返回 true**，所以每笔椒图消费都会去建行→撞 CHECK→刷一条
   `ALERT: incr user platform quota DB failed ... user_platform_quotas_platform_check`；
   开了 flusher 后更糟：脏项会进入 `BatchSnapshotUsage` 的单条多行 UPSERT，**一行违约整批失败**。
   现已用 `service.ShouldAccumulateUserPlatformQuota(platform)` 在两处计费入口统一收口。
   如果确实要让椒图进入按用户限额体系，需同时动三处（新迁移 + `AllowedQuotaPlatforms` +
   ent schema 并 `go generate ./ent`），不要只改其中一处。

### 状态机口径

| 上游/内部错误 | 动作 |
|---|---|
| 积分不足（预检或 SSE `insufficient_points`） | `points=0` + `temp_unschedulable_until=now+10min`，保持 `active/schedulable`（充值或签到可恢复） |
| 401/403 | `jiaotu_status=expired` + `schedulable=false` + `status=inactive`（token 无 refresh，只能人工处理） |
| 上游 5xx / 超时 / 空结果 | **不惩罚账号**，仅换号重试 |
| 参考图数量/格式/体积、`seed`、参考音频、size 非法 | 入站 400，选号之前拦下，**不消耗积分** |

### 管理端与创作中心

- **手填单个账号**：「添加账号」弹窗 →「国产供应商」行末尾的 `椒图` 按钮
  （`CreateAccountModal.selectJiaotuPlatform()`）。表单只收 token（必填）+ 手机号 / 号池 ID /
  昵称 / 积分快照（均可选），**不送 `base_url`/`api_key`**（后端 `Account.JiaotuToken()` 只读
  `credentials.token`）；选中时强制 `type=apikey` 且并发默认 1。
- **号池批量导入 + 全池维护**：账号管理 → 更多操作 → `导入椒图号池`
  （`components/admin/account/JiaotuPoolImportModal.vue`）。同一弹窗里粘贴 JSON →
  `POST /admin/accounts/import/jiaotu-pool`，并可调 `POST /admin/accounts/jiaotu/maintenance`
  （刷积分 / 可选签到）。分组下拉只列 `platform=jiaotu` 的分组。
- **列表积分**：「用量」列对椒图渲染 `components/account/JiaotuPointsCell.vue`
  （积分快照 + `jiaotu_status` 徽标 + 号池 ID/手机号）。它**自身不发请求**，
  否则翻页就会按页打上游；刷新走上面的维护接口。
- **凭据读写**：统一走 `credentialsBuilder.ts` 的 `buildJiaotuCredentials()` /
  `readJiaotuAccountView()`。`credentials.token` 会被 `RedactCredentials` 从响应里剔掉，
  所以前端只能用 `credentials_status.has_token` 判断存在性，**不要试图回填 token**。
- **创作中心**：两个模型下拉框都由 `listCreativeModels(apiKey, { capability })` 经 `GET /v1/models` 驱动
  （`listCreativeVideoModels` 是它的 video 专用包装）。关键点：椒图的图片与视频稳定 ID
  **共用 `jiaotu-` 前缀**，只能按 `capability` 分家；按前缀猜会把 `jiaotu-minimax-h3` 递到
  `/v1/images` 吃 400（已用 `src/api/__tests__/creative.models.spec.ts` 锁住）。
  网关不返 capability 时才回退到 id 前缀，且只回退给图片；视频拉不到列表时保留
  `sora-1` / `grok-imagine-video` 两个写死选项，行为与接入椒图前一致。
- **接入新平台的连带清单**（这次踩到的）：`types/index.ts` → `constants/platforms.ts` →
  `utils/platformColors.ts`（14 张颜色表 + `isPlatform` + `platformLabel`）→
  `constants/__tests__/platforms.spec.ts` 的平台清单→ `CreateAccountModal` / `AccountUsageCell` 分派 →
  `{zh,en}/admin/accounts.ts` 的 i18n（`check:i18n` 会扫源码里的静态 `t()` 键）。

### 验证命令

```bash
cd backend
go test -tags=unit -run Jiaotu ./internal/service/ ./internal/handler/
go build ./... && go vet ./...

cd ../frontend
node node_modules/vitest/vitest.mjs run \
  src/components/account/__tests__/JiaotuPoolComponents.spec.ts \
  src/components/account/__tests__/credentialsBuilder.jiaotu.spec.ts \
  src/components/account/__tests__/CreateAccountModal.spec.ts \
  src/i18n/__tests__/localeKeyCompleteness.spec.ts
node node_modules/vue-tsc/bin/vue-tsc.js --noEmit
```

> 本机若没装 `pnpm`，直接用 `node node_modules/<pkg>` 跑同一条命令即可（仓库约定依赖仍只能由 pnpm 写入）。
> 新平台接入时记得同步 `src/constants/__tests__/platforms.spec.ts` 的平台清单，否则 `lint:check` 会绿但
> `vitest` 会报「exposes every concrete account platform」。

## 七、参考资源

- [上游仓库](https://github.com/Wei-Shaw/sub2api)
- [Ent 文档](https://entgo.io/docs/getting-started)
- [Vue3 文档](https://vuejs.org/)
- [pnpm 文档](https://pnpm.io/)
