# Sepiida

用于采集MiniWDL分析状态的系统。

## 目录结构设计

MiniWDL使用 `-d uuid` 模式执行时，目录结构如下：

```
/mnt/data/output/
├── uuid-001/                     # 样本UUID目录（标准UUID格式）
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

**UUID格式验证：** Agent只识别标准UUID格式的目录名（如 `a1b2c3d4-e5f6-7890-abcd-ef1234567890`）

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
│       ├── db/           # 数据库接口 (SQLite/PostgreSQL)
│       └── apikey/       # 动态Key管理
├── keys.example          # Key文件示例
├── Makefile
├── go.mod
└ README.md
```

## 功能

### Server端
- 接收Agent推送的MiniWDL分析进度
- 支持SQLite和PostgreSQL数据库
- **动态Key管理**：从文件读取API Key，定时刷新
- 提供查询API（支持UUID查询）

### Agent端
- 监控UUID目录下的 `_LAST` 软链接获取最新执行
- 验证UUID目录名格式
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

### 2. 准备Key文件

创建key文件（如 `keys.txt`）：

```bash
# 复制示例文件
cp keys.example keys.txt

# 编辑keys.txt，添加你的API Key
# 每行一个key，#开头为注释
echo "your-secret-key-1" >> keys.txt
echo "your-secret-key-2" >> keys.txt
```

Key文件格式：
```
# 这是注释，会被忽略
key-for-agent-001
key-for-agent-002
key-for-agent-003
```

### 3. 启动Server

```bash
# SQLite + Key文件
./bin/sepiida-server -p 8080 -d sqlite://data/sepiida.db -key keys.txt

# PostgreSQL + Key文件
./bin/sepiida-server -p 8080 -d postgres://localhost:5432/sepiida?user=postgres&password=xxx -key keys.txt

# 自定义Key刷新间隔（默认30秒）
./bin/sepiida-server -p 8080 -d sqlite://data/sepiida.db -key keys.txt -key-refresh 60
```

**参数说明：**
| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-p` | 监听端口 | 8080 |
| `-d` | 数据库连接字符串 | sqlite://data/sepiida.db |
| `-key` | Key文件路径 | **必填** |
| `-key-refresh` | Key文件刷新间隔（秒） | 30 |

**数据库格式：**
- SQLite: `sqlite://path/to/db.db`
- PostgreSQL: `postgres://host:port/database?user=xxx&password=xxx`

### 4. 启动Agent

```bash
# 使用Key文件中的某个key
./bin/sepiida-agent -s http://localhost:8080 -key your-secret-key-1 -id agent-001 -w /mnt/data/output
```

**参数说明：**
| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-s` | Server URL | http://localhost:8080 |
| `-key` | API Key（需在Server的Key文件中） | （需指定） |
| `-id` | Agent ID | agent-001 |
| `-i` | 推送间隔（秒） | 60 |
| `-w` | 监控目录（UUID目录的父目录） | ./output |

## 动态Key管理

Server会定时读取Key文件，支持动态更新：

1. **自动刷新**：默认每30秒检查Key文件是否有修改
2. **文件修改检测**：通过文件修改时间判断是否需要重新加载
3. **即时生效**：修改Key文件后，最多等待刷新间隔即可生效

**管理API：**
```
GET /keys/status    # 查看Key状态（key数量、文件路径、刷新间隔）
POST /keys/reload   # 强制立即重新加载Key文件
```

**示例：**
```bash
# 查看Key状态
curl http://localhost:8080/keys/status

# 强制重新加载
curl -X POST http://localhost:8080/keys/reload
```

## API接口

### POST /api/v1/progress
接收Agent推送的进度数据，包含UUID

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

### GET /keys/status
查看Key状态（无需认证）

### POST /keys/reload
强制重新加载Key文件（无需认证）

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

Agent在每个UUID目录下维护 `.sepiida.json` 状态文件，记录：
- Workflow当前状态
- 各Task状态快照
- 上次推送时间
- outputs.json是否已推送
- workflow.log文件大小和修改时间
- 当前执行目录路径

## 依赖

- Go 1.21+
- SQLite3 或 PostgreSQL

## License

MIT