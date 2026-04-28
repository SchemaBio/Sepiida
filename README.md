# Sepiida

用于采集MiniWDL分析状态的系统。

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
- 支持SQLite和PostgreSQL数据库
- **双重Key管理**：
  - Agent Key：用于Agent推送数据
  - Query Key：用于查询结果
- Key文件动态刷新

### Agent端
- 监控UUID目录下的 `_LAST` 软链接获取最新执行
- 验证UUID目录名格式
- 解析MiniWDL workflow.log日志
- 收集Workflow和Task状态信息
- 收集stdout/stderr日志
- 收集outputs.json结果文件
- **增量推送**：只推送有变化的Workflow

## 使用方法

### 1. 编译

```bash
make build
```

### 2. 准备Key文件

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

### 3. 启动Server

```bash
# SQLite
./bin/sepiida-server -p 8080 -d sqlite://data/sepiida.db \
    -agent-key agent-keys.txt \
    -query-key query-keys.txt

# PostgreSQL
./bin/sepiida-server -p 8080 -d postgres://localhost:5432/sepiida?user=postgres&password=xxx \
    -agent-key agent-keys.txt \
    -query-key query-keys.txt

# 自定义刷新间隔（默认30秒）
./bin/sepiida-server -p 8080 -d sqlite://data/sepiida.db \
    -agent-key agent-keys.txt \
    -query-key query-keys.txt \
    -key-refresh 60
```

**参数说明：**
| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-p` | 监听端口 | 8080 |
| `-d` | 数据库连接字符串 | sqlite://data/sepiida.db |
| `-agent-key` | Agent Key文件路径 | **必填** |
| `-query-key` | Query Key文件路径 | **必填** |
| `-key-refresh` | Key文件刷新间隔（秒） | 30 |

### 4. 启动Agent

```bash
# Agent使用agent-keys.txt中的某个key
./bin/sepiida-agent -s http://localhost:8080 \
    -key my-agent-key-001 \
    -id agent-001 \
    -w /mnt/data/output
```

**参数说明：**
| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-s` | Server URL | http://localhost:8080 |
| `-key` | API Key（需在agent-keys.txt中） | （需指定） |
| `-id` | Agent ID | agent-001 |
| `-i` | 推送间隔（秒） | 60 |
| `-w` | 监控目录（UUID目录的父目录） | ./output |

### 5. 查询结果

使用query-keys.txt中的Key查询：

```bash
# 查询指定UUID的Workflow
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:8080/api/v1/workflow?uuid=a1b2c3d4-e5f6-7890-abcd-ef1234567890"

# 列出所有Workflows
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:8080/api/v1/workflows"

# 查询Workflow的Tasks
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:8080/api/v1/workflow/tasks?id=20260428_094955_SingleWES"
```

## API接口

### Agent API（需要Agent Key）

| 接口 | 说明 |
|------|------|
| `POST /api/v1/progress` | 推送进度数据 |
| `POST /api/v1/workflow/output` | 推送outputs.json |

### Query API（需要Query Key）

| 接口 | 说明 |
|------|------|
| `GET /api/v1/workflow?uuid=xxx` | 通过UUID查询 |
| `GET /api/v1/workflow?id=xxx` | 通过ID查询 |
| `GET /api/v1/workflow/tasks?id=xxx` | 查询Tasks |
| `GET /api/v1/workflows` | 列出所有Workflows |

### 管理API（无需认证）

| 接口 | 说明 |
|------|------|
| `GET /health` | 健康检查 |
| `GET /keys/status` | 查看Key状态 |
| `POST /keys/reload` | 强制重新加载Key文件 |

## 双重Key机制

| Key类型 | 用途 | Key文件 |
|---------|------|---------|
| Agent Key | Agent推送进度数据 | `-agent-key` 参数 |
| Query Key | 查询Workflow结果 | `-query-key` 参数 |

**设计目的：**
- Agent只能推送数据，不能查询
- 查询用户只能查看结果，不能推送
- 两类Key分开管理，安全性更高

## Key动态刷新

- Server定时检查Key文件修改时间
- 文件修改后自动重新加载
- 修改Key文件后最多等待刷新间隔即可生效
- 可通过 `POST /keys/reload` 强制立即刷新

## 数据库设计

```sql
CREATE TABLE workflows (
    id TEXT PRIMARY KEY,
    uuid TEXT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    start_time TIMESTAMP,
    end_time TIMESTAMP,
    output_dir TEXT,
    outputs_json TEXT,
    agent_id TEXT,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
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

- Go 1.21+
- SQLite3 或 PostgreSQL

## License

MIT