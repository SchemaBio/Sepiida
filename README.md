# Sepiida

**简体中文** | [English](./README.en.md)

用于采集[MiniWDL](https://github.com/chanzuckerberg/miniwdl)分析状态的系统。

## 目录结构设计

MiniWDL使用 `-d uuid` 模式执行时，目录结构如下：

```
/mnt/data/output/
├── a1b2c3d4-e5f6-7890-abcd-ef1234567890/    # 样本UUID目录（标准UUID格式）
│   ├── _LAST -> 20260428_094955_SingleWES/  # 软链接指向最新执行
│   ├── 20260428_094955_SingleWES/           # 执行目录
│   │   ├── workflow.log          # MiniWDL日志
│   │   ├── outputs.json          # 最终输出
│   │   └── call-CreateMitoBed/   # Task输出目录
│   │       ├── stdout.txt
│   │       └── stderr.txt
│   ├── 20260427_xxxxxx_SingleWES/            # 之前的执行（如有）
│   └── .sepiida.json             # Agent状态文件（在UUID目录）
├── b2c3d4e5-f6a7-8901-bcde-f23456789012/
│   ├── _LAST -> ...
│   └── ...
```

**UUID格式验证：** Agent只识别标准UUID格式的目录名（如 `a1b2c3d4-e5f6-7890-abcd-ef1234567890`）

## 功能

### Server端
- 接收Agent推送的MiniWDL分析进度
- 使用PostgreSQL数据库
- **双重Key管理**：
  - Agent Key：用于Agent推送数据
  - Query Key：用于查询结果
- Key文件动态刷新
- **明确认证模式**：`task-token` 用于 SaaS，`static` 用于自部署和本地开发

### Agent端
- 监控UUID目录下的 `_LAST` 软链接获取最新执行
- 验证UUID目录名格式
- 解析MiniWDL workflow.log日志
- 收集Workflow和Task状态信息
- 收集stdout/stderr日志
- 收集outputs.json结果文件
- **增量推送**：只推送有变化的Workflow
- **对象存储归档**：Workflow成功后自动归档输出文件到S3/MinIO/OSS/COS或本地目录

## 使用方法

### 1. Docker Compose 部署（推荐）

**准备环境变量：**

```bash
cp .env.example .env
# 编辑 .env，设置 DATABASE_URL、Key 等
```

`.env` 关键配置：
```bash
# 指向你的外部 Postgres 实例
DATABASE_URL=postgres://user:password@host:5432/sepiida?sslmode=disable

# Server 对外端口
SERVER_PORT=9090
```

Sepiida 的 Docker 镜像只包含 Server。Agent 需要直接运行在能够访问
MiniWDL 输出目录的宿主机或计算节点上，不作为应用镜像发布。

**启动 Server：**
```bash
docker compose build
docker compose up -d
```

### 2. 编译（本地运行）

```bash
make build
```

### 3. 准备Key文件

计算节点镜像中的 Agent 安装方法见下方「CVM 镜像安装」。

创建两个Key文件：

```bash
# Agent Key文件（用于Agent推送数据）
cp agent-keys.example agent-keys.txt
# 编辑添加Agent的Key
echo "my-agent-key-001" >> agent-keys.txt

# Query Key文件（用于查询结果）
cp query-keys.example query-keys.txt
# 编辑添加查询的Key
echo "my-query-key-001" >> query-keys.txt
```

**Key文件格式：**
```
# 这是注释，会被忽略
key-001
key-002
key-003
```

### 4. 启动Server

```bash
./bin/sepiida-server -p 9090 \
    -d "postgres://localhost:5432/sepiida?user=postgres&password=xxx" \
    -agent-key agent-keys.txt \
    -query-key query-keys.txt \
    -auth-mode static

# 自定义刷新间隔（默认30秒）
./bin/sepiida-server -p 9090 \
    -d "postgres://localhost:5432/sepiida?user=postgres&password=xxx" \
    -agent-key agent-keys.txt \
    -query-key query-keys.txt \
    -auth-mode static \
    -key-refresh 60
```

> 自部署路线使用 `static`；SaaS 路线使用 `task-token` 并配置至少 32 字符的
> `SEPIIDA_TASK_TOKEN_SECRET`，由 Squid 为每个任务签发写入令牌。

SaaS `task-token` 的撤销记录持久化在 Sepiida PostgreSQL 的
`task_token_revocations` 表中，Server 重启或多实例部署不会恢复已撤销令牌。
撤销状态查询发生数据库错误时，Agent 写入会拒绝并返回服务不可用，避免在
无法确认撤销状态时继续接收进度。生产环境应为 Server 配置独立数据库角色，
并将 `SEPIIDA_TASK_TOKEN_SECRET` 与 Squid 保持一致。

**参数说明：**
| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-p` | 监听端口 | 9090 |
| `-d` | 数据库连接字符串 | postgres://localhost:5432/sepiida?user=postgres&password=postgres |
| `-agent-key` | Agent Key文件路径 | `static` 模式必填 |
| `-query-key` | Query Key文件路径 | **必填** |
| `-task-token-secret` | 每任务写入令牌的 HMAC 共享密钥（SaaS 与 Squid 一致） | 空 |
| `-auth-mode` | `static` 或 `task-token` | task-token |
| `-key-refresh` | Key文件刷新间隔（秒） | 30 |

回调限流按来源拆分：服务间撤销请求按 IP 默认 600 次/分钟，Agent 进度、输出和
归档回调按执行令牌默认 60 次/分钟。可通过
`SEPIIDA_SERVICE_CALLBACK_RATE_LIMIT_PER_MINUTE` 和
`SEPIIDA_NODE_CALLBACK_RATE_LIMIT_PER_MINUTE` 调整，超限响应带
`Retry-After`；同一执行的正常回调不会与登录或查询请求竞争额度。

### 5. 启动Agent

```bash
# Agent使用agent-keys.txt中的某个key
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 \
    -id agent-001 \
    -w /mnt/data/output
```

**参数说明：**
| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-s` | Server URL | http://localhost:9090 |
| `-key` | API Key（需在agent-keys.txt中） | （需指定） |
| `-task-token` | Squid 签发的任务令牌，与 `-key` 二选一 | `SEPIIDA_TASK_TOKEN` |
| `-node-secret` | Cloudflare 规则匹配的 `x-node-secret` 请求头；同时用于回调和 COS 临时凭据续期 | `SEPIIDA_NODE_SECRET`，回退到 `CVM_NODE_SECRET` |
| `-version` | 输出构建提交、工作区修改标记、Go 版本和目标平台后退出，无需令牌 | — |
| `-id` | Agent ID | agent-001 |
| `-i` | 推送间隔（秒） | 60 |
| `-w` | 监控目录（UUID目录的父目录） | ./output |
| `-archive` | 对象存储归档路径（见下方说明） | （不归档） |
| `-archive-prefix` | 当前执行尝试的对象存储前缀（标准 UUID）；为空时使用监控目录 UUID | （使用 UUID） |
| `-archive-key-id` | 对象存储 Access Key ID（覆盖环境变量） | （读取环境变量） |
| `-archive-key-secret` | 对象存储 Secret Access Key（覆盖环境变量） | （读取环境变量） |

#### CVM 镜像安装

在源码目录执行 `make build-agent-linux`，生成不依赖动态 C 运行库的 Linux amd64 Agent。
Windows PowerShell 可执行 `./scripts/build-agent.ps1`；ARM64 镜像使用
`./scripts/build-agent.ps1 -Architecture arm64`。产物和校验文件位于 `bin/`。

将 `sepiida-agent-linux-amd64` 和对应的 `.sha256` 文件传到镜像制作节点后执行：

```bash
sha256sum -c sepiida-agent-linux-amd64.sha256
sudo install -m 0755 sepiida-agent-linux-amd64 /usr/local/bin/sepiida-agent
/usr/local/bin/sepiida-agent --version
command -v sepiida-agent
```

Agent 由 Squid 生成的 UserData 启动。镜像中只安装程序，不保存任务令牌、
节点密钥、COS 临时凭据或运行中的任务状态。`--version` 的 `modified=true`
表示二进制构建时包含未提交的修改，不能只凭提交号识别该产物；使用 SHA-256 核对文件。

SaaS 启动时，Squid 注入 `SEPIIDA_TASK_TOKEN`、`SEPIIDA_CREDENTIALS_URL`，
以及配置了 `CVM_NODE_SECRET` 时的 `SEPIIDA_NODE_SECRET`。任务令牌仍用于应用认证，
节点密钥用于匹配 Cloudflare 的 API 放行规则。Agent 的进度、输出、归档通知和
COS 凭据续期使用相同的任务令牌及节点密钥，命令行参数优先于环境变量。
密钥请求头不会发送到 COS 对象存储。认证请求拒绝跟随重定向，服务器 URL 应直接指向 API。

Cloudflare 需要配置相应规则后才能放行；仅更新 Agent 不会改变边缘规则。
若请求被拦截，Agent 日志会记录 HTTP 状态码和响应中的 `CF-Ray`，用于查询 Security Events。
空输出目录下的正常轮询只能验证 Agent 启动，仍需真实工作流验证进度和归档。

### 6. 对象存储归档（可选）

Agent 可以在 Workflow 成功完成后，自动将输出文件归档到对象存储或本地目录。通过 `-archive` 参数指定归档目标。

**使用示例：**

```bash
# 归档到 AWS S3（通过参数指定凭据）
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive s3://my-bucket/prefix \
    -archive-key-id AKIA... \
    -archive-key-secret ...

# 归档到 MinIO（通过参数指定凭据）
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive http://minio.local:9000/my-bucket/prefix \
    -archive-key-id minioadmin \
    -archive-key-secret minioadmin

# 归档到阿里云 OSS
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive oss://cn-hangzhou/my-bucket/prefix \
    -archive-key-id LTAI... \
    -archive-key-secret ...

# 归档到腾讯云 COS（虚拟托管域名）
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive https://mybucket-1250000000.cos.ap-guangzhou.myqcloud.com/prefix \
    -archive-key-id AKID... \
    -archive-key-secret ...

# 归档到腾讯云 COS（短URL格式，等价于上面）
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive cos://ap-guangzhou/mybucket-1250000000/prefix \
    -archive-key-id AKID... \
    -archive-key-secret ...

# 也可以通过环境变量指定凭据（不使用 -archive-key-* 参数时自动读取）
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive s3://my-bucket/prefix

# 归档到本地目录（无需凭据）
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive /mnt/archive/outputs
```

**支持的存储后端：**

| URL格式 | 存储系统 | 示例 |
|---------|---------|------|
| `s3://bucket/prefix` | AWS S3 | `s3://sepiida-archive/results` |
| `http://host:port/bucket/prefix` | MinIO (HTTP) | `http://minio.local:9000/results` |
| `https://host:port/bucket/prefix` | MinIO (HTTPS) | `https://minio.example.com/results` |
| `oss://region/bucket/prefix` | 阿里云 OSS | `oss://cn-hangzhou/my-bucket/results` |
| `cos://region/bucket/prefix` | 腾讯云 COS（短URL） | `cos://ap-guangzhou/mybucket-1250000000/results` |
| `https://<bucket>.cos.<region>.myqcloud.com/prefix` | 腾讯云 COS（虚拟托管） | `https://mybucket-1250000000.cos.ap-guangzhou.myqcloud.com/results` |
| 本地路径 | 本地文件系统 | `/mnt/archive/outputs` |

**认证凭据：**

优先使用 `-archive-key-id` / `-archive-key-secret` 参数，未指定时自动从环境变量读取：

| 存储系统 | CLI 参数 | 环境变量（备选） |
|---------|---------|---------|
| AWS S3 / MinIO | `-archive-key-id` + `-archive-key-secret` | `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` |
| MinIO | 同上 | `MINIO_ROOT_USER` + `MINIO_ROOT_PASSWORD` |
| 阿里云 OSS | 同上 | `ALIBABA_CLOUD_ACCESS_KEY_ID` + `ALIBABA_CLOUD_ACCESS_KEY_SECRET` |
| 腾讯云 COS | 同上 | `COS_SECRET_ID` + `COS_SECRET_KEY` |

**归档内容：**

当 Workflow 状态变为 `success` 且尚未归档时，Agent 自动上传以下文件：

| 文件 | 处理方式 | 存储 Key |
|------|---------|----------|
| `workflow.log` | 直接上传 | `{uuid}/workflow.log` |
| `outputs.json` | 直接上传 | `{uuid}/outputs.json` |
| `inputs.json` | 直接上传 | `{uuid}/inputs.json` |
| `.txt` / `.csv` / `.tsv` 文件 | 转换为独立 Parquet 文件 | `{uuid}/{文件名}.parquet` |
| 其他文件（.bam, .bai, .vcf.gz 等） | 扁平化上传（仅文件名） | `{uuid}/{文件名}` |

> **扁平化归档：** `outputs.json` 中引用的所有文件均使用扁平化命名，仅保留文件名（basename），不再保持原有目录结构。如有同名文件，自动添加序号（如 `result.bam`、`result_2.bam`）。

> **Parquet 动态 Schema：** 每个文本文件（`.txt`、`.csv`、`.tsv`）独立转换为 Parquet 文件。第一行作为标题行，列名成为 Parquet 的字段名，后续每行数据作为独立记录存储。例如 `cnv.txt` 内容为 `gene,type,value` 三列，转换后 `{uuid}/cnv.parquet` 拥有对应的 `gene`、`type`、`value` 三列，可直接按列查询。`outputs.resolved.json` 中的路径也会更新为 `.parquet` 扩展名。

**幂等性：** 每个 UUID 目录下的 `.sepiida.json` 状态文件会记录 `Archived` 标志，防止重复归档。

### 7. 查询结果

使用query-keys.txt中的Key查询：

```bash
# 查询指定UUID的Workflow
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:9090/api/v1/workflow?uuid=a1b2c3d4-e5f6-7890-abcd-ef1234567890"

# 列出所有Workflows
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:9090/api/v1/workflows"

# 查询Workflow的Tasks
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:9090/api/v1/workflow/tasks?id=20260428_094955_SingleWES"
```

## API接口

### Agent API（需要 Agent Key 或 Task Token）

| 接口 | 说明 |
|------|------|
| `POST /api/v1/progress` | 推送进度数据 |
| `POST /api/v1/workflow/output` | 推送outputs.json |
| `POST /api/v1/workflow/archive` | 报告归档完成状态 |

### Query API（需要Query Key）

| 接口 | 说明 |
|------|------|
| `GET /api/v1/workflow?uuid=xxx` | 通过UUID查询 |
| `GET /api/v1/workflow?id=xxx` | 通过ID查询 |
| `GET /api/v1/workflow/tasks?id=xxx` | 查询Tasks |
| `GET /api/v1/workflows` | 列出所有Workflows |
| `GET /api/v1/keys/status` | 查看Key状态 |
| `POST /api/v1/keys/reload` | 强制重新加载Key文件 |

### 公开API（无需认证）

| 接口 | 说明 |
|------|------|
| `GET /health` | 健康检查 |

## 双重Key机制

| Key类型 | 用途 | Key文件 |
|---------|------|---------|
| Agent Key | Agent推送进度数据 | `-agent-key` 参数 |
| Query Key | 查询Workflow结果 | `-query-key` 参数 |

**设计目的：**
- Agent只能推送数据，不能查询
- 查询用户只能查看结果，不能推送
- 两类Key分开管理，安全性更高

Query Key 默认兼容旧格式，每行一个 key，拥有全量查询权限。生产环境建议使用 scoped query key，将 key 限定到具体 workflow UUID 或 workflow ID：

```text
# 全量查询权限
my-query-key-001

# 只允许查询指定 workflow UUID / workflow ID；不能调用 /api/v1/workflows 或 key 管理接口
my-scoped-query-key uuid=workflow-uuid-1,workflow-uuid-2
my-workflow-query-key workflow=workflow-id-1
```

## Key动态刷新

- Server定时检查Key文件修改时间
- 文件修改后自动重新加载
- 修改Key文件后最多等待刷新间隔即可生效
- 可通过 `POST /api/v1/keys/reload` 强制立即刷新（需Query Key认证）

**示例：**
```bash
# 强制重新加载Key文件（需要Query Key）
curl -X POST -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:9090/api/v1/keys/reload"
```

## 数据库设计

```sql
CREATE TABLE workflows (
    id TEXT PRIMARY KEY,
    uuid TEXT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    start_time TIMESTAMPTZ,
    end_time TIMESTAMPTZ,
    output_dir TEXT,
    outputs_json JSONB,
    agent_id TEXT,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);
CREATE INDEX idx_workflows_uuid ON workflows(uuid);

CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    uuid TEXT NOT NULL,
    name TEXT NOT NULL,
    job_name TEXT NOT NULL,
    status TEXT NOT NULL,
    ...
);
CREATE INDEX idx_tasks_uuid ON tasks(uuid);
```

## 依赖

- Go 1.25+
- PostgreSQL
- [minio-go](https://github.com/minio/minio-go) - S3兼容对象存储客户端（归档功能）
- [parquet-go](https://github.com/parquet-go/parquet-go) - Parquet文件写入（文本文件合并）

## License

MIT
