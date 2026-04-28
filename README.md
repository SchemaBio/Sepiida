# Sepiida

用于采集MiniWDL分析状态的系统。

## 目录结构设计

MiniWDL使用 `-d uuid` 模式执行时，目录结构如下：

```
/mnt/data/output/
├── uuid-001/                     # 样本UUID目录
│   ├── _LAST -> 20260428_094955_SingleWES/   # 软链接指向最新执行
│   ├── 20260428_094955_SingleWES/            # 执行目录
│   │   ├── workflow.log          # MiniWDL日志
│   │   ├── outputs.json          # 最终输出
│   │   └── call-CreateMitoBed/   # Task输出目录
│   │       ├── stdout.txt
│   │       └── stderr.txt
│   ├── 20260427_xxxxxx_SingleWES/            # 之前的执行（如有）
│   └── .sepiida.json             # Agent状态文件（在UUID目录）
├── uuid-002/
│   ├── _LAST -> ...
│   └── ...
```

## 项目结构

```
Sepiida/
├── cmd/
│   ├── server/           # Server端入口
│   └── agent/            # Agent端入口
├── internal/
│   ├── server/           # Server端实现
│   │   ├── handler/      # HTTP处理器
│   │   ├── service/      # Workflow服务
│   │   └── middleware/   # 认证中间件
│   ├── agent/            # Agent端实现
│   │   ├── parser/       # MiniWDL日志解析器
│   │   ├── collector/    # 进度收集器
│   │   ├── sender/       # HTTP发送器
│   │   └── state/        # 状态管理（增量推送）
│   └── common/
│       ├── model/        # 数据模型
│       └── db/           # 数据库接口 (SQLite/PostgreSQL)
├── Makefile
├── go.mod
└ README.md
```

## 功能

### Server端
- 接收Agent推送的MiniWDL分析进度
- 支持SQLite和PostgreSQL数据库
- API Key认证
- 提供查询API（支持UUID查询）

### Agent端
- 监控UUID目录下的 `_LAST` 软链接获取最新执行
- 解析MiniWDL workflow.log日志
- 收集Workflow和Task状态信息
- 收集stdout/stderr日志
- 收集outputs.json结果文件
- 状态文件存储在UUID目录（`.sepiida.json`）
- **增量推送**：只推送有变化的Workflow

## 使用方法

### 1. 编译

```bash
make build
# 或分别编译
make build-server
make build-agent
```

### 2. 启动Server

```bash
# SQLite
./bin/sepiida-server -p 8080 -d sqlite://data/sepiida.db -key your-secret-key

# PostgreSQL  
./bin/sepiida-server -p 8080 -d postgres://localhost:5432/sepiida?user=postgres&password=xxx -key key1,key2
```

**参数说明：**
| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-p` | 监听端口 | 8080 |
| `-d` | 数据库连接字符串 | sqlite://data/sepiida.db |
| `-key` | API Key（逗号分隔） | default-api-key |

**数据库格式：**
- SQLite: `sqlite://path/to/db.db`
- PostgreSQL: `postgres://host:port/database?user=xxx&password=xxx`

### 3. 启动Agent

```bash
# 监控包含UUID目录的父目录
./bin/sepiida-agent -s http://localhost:8080 -key your-secret-key -id agent-001 -i 60 -w /mnt/data/output
```

Agent会在 `/mnt/data/output` 下寻找UUID目录，通过 `_LAST` 软链接获取最新执行。

**参数说明：**
| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-s` | Server URL | http://localhost:8080 |
| `-key` | API Key | （需指定） |
| `-id` | Agent ID | agent-001 |
| `-i` | 推送间隔（秒） | 60 |
| `-w` | 监控目录（UUID目录的父目录） | ./output |

## API接口

### POST /api/v1/progress
接收Agent推送的进度数据，包含UUID

**请求体：**
```json
{
  "agent_id": "agent-001",
  "uuid": "uuid-001",
  "workflow": {
    "id": "20260428_094955_SingleWES",
    "uuid": "uuid-001",
    "name": "SingleWES",
    "status": "running"
  },
  "tasks": [...]
}
```

### POST /api/v1/workflow/output
接收Workflow的outputs.json

### GET /api/v1/workflow?uuid=xxx
通过UUID查询Workflow（推荐）

### GET /api/v1/workflow?id=xxx
通过Workflow ID查询

### GET /api/v1/workflow/tasks?id=xxx
查询Workflow的所有Tasks

### GET /api/v1/workflows
列出所有Workflows

### GET /health
健康检查（无需认证）

## 数据库设计

### workflows 表

```sql
CREATE TABLE workflows (
    id TEXT PRIMARY KEY,              -- 执行ID (如: 20260428_094955_SingleWES)
    uuid TEXT NOT NULL,               -- 样本UUID（关键字段）
    name TEXT NOT NULL,               -- Workflow名称
    status TEXT NOT NULL,             -- 状态: running/success/failed
    start_time TIMESTAMP,
    end_time TIMESTAMP,
    output_dir TEXT,
    outputs_json TEXT,                -- outputs.json内容
    agent_id TEXT,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);
CREATE INDEX idx_workflows_uuid ON workflows(uuid);
```

### tasks 表

```sql
CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    uuid TEXT NOT NULL,               -- 样本UUID
    name TEXT NOT NULL,
    job_name TEXT NOT NULL,
    status TEXT NOT NULL,
    ...
);
CREATE INDEX idx_tasks_uuid ON tasks(uuid);
```

## 状态文件机制

Agent在每个UUID目录下维护 `.sepiida.json` 状态文件：

```json
{
  "uuid": "uuid-001",
  "workflow_id": "20260428_094955_SingleWES",
  "workflow_status": "running",
  "execution_dir": "/mnt/data/output/uuid-001/20260428_094955_SingleWES",
  "last_pushed_at": "2026-04-28T10:00:00Z",
  "outputs_pushed": false,
  "task_states": {...},
  "log_file_size": 12345,
  "log_file_mod_time": "2026-04-28T10:00:00Z"
}
```

**变化检测逻辑：**
- `_LAST` 指向新的执行目录（新运行开始）
- workflow.log文件被修改
- Workflow状态变化
- Task状态变化
- outputs.json未推送但workflow已完成

## 依赖

- Go 1.21+
- SQLite3 或 PostgreSQL

## License

MIT