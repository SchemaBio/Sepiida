# Sepiida

[简体中文](./README.md) | **English**

Sepiida collects analysis status from
[MiniWDL](https://github.com/chanzuckerberg/miniwdl) executions.

## Output Directory Layout

When MiniWDL runs with `-d uuid`, Sepiida expects the following layout:

```text
/mnt/data/output/
|-- a1b2c3d4-e5f6-7890-abcd-ef1234567890/    # Sample UUID directory
|   |-- _LAST -> 20260428_094955_SingleWES/  # Symlink to the latest run
|   |-- 20260428_094955_SingleWES/           # Execution directory
|   |   |-- workflow.log                     # MiniWDL log
|   |   |-- outputs.json                     # Final outputs
|   |   `-- call-CreateMitoBed/              # Task output directory
|   |       |-- stdout.txt
|   |       `-- stderr.txt
|   |-- 20260427_xxxxxx_SingleWES/           # Earlier execution, if any
|   `-- .sepiida.json                        # Agent state for this UUID
`-- b2c3d4e5-f6a7-8901-bcde-f23456789012/
    |-- _LAST -> ...
    `-- ...
```

**UUID validation:** The Agent only recognizes directory names that use the
standard UUID format, such as
`a1b2c3d4-e5f6-7890-abcd-ef1234567890`.

## Features

### Server

- Receives MiniWDL progress reported by Agents.
- Stores workflow and task state in PostgreSQL.
- Separates credentials by capability:
  - Agent keys authorize progress writes.
  - Query keys authorize result reads.
- Reloads key files dynamically.
- Supports explicit authentication modes: `task-token` for SaaS and
  `static` for self-hosted and local development.

### Agent

- Watches the `_LAST` symlink in each UUID directory.
- Validates UUID directory names.
- Parses MiniWDL `workflow.log`.
- Collects workflow and task state.
- Collects `stdout`, `stderr`, and `outputs.json`.
- Sends updates only when workflow state changes.
- Archives successful workflow outputs to S3, MinIO, Alibaba Cloud OSS,
  Tencent Cloud COS, or a local directory.

## Usage

### 1. Deploy the Server with Docker Compose

Prepare the environment:

```bash
cp .env.example .env
# Edit .env and configure DATABASE_URL and the required keys.
```

Important `.env` settings:

```bash
# External PostgreSQL instance
DATABASE_URL=postgres://user:password@host:5432/sepiida?sslmode=disable

# Published Server port
SERVER_PORT=9090
```

The Sepiida Docker image contains only the Server. Run the Agent directly on
the host or compute node that can access the MiniWDL output directories. The
Agent is not packaged in the application image.

Start the Server:

```bash
docker compose build
docker compose up -d
```

### 2. Build for Local Use

```bash
make build
```

### 3. Prepare Key Files

Create separate files for write and query credentials:

```bash
# Agent keys authorize progress writes.
cp agent-keys.example agent-keys.txt
echo "my-agent-key-001" >> agent-keys.txt

# Query keys authorize result reads.
cp query-keys.example query-keys.txt
echo "my-query-key-001" >> query-keys.txt
```

Key files contain one key per line. Empty lines and comments are ignored:

```text
# This is a comment.
key-001
key-002
key-003
```

### 4. Start the Server

```bash
./bin/sepiida-server -p 9090 \
    -d "postgres://localhost:5432/sepiida?user=postgres&password=xxx" \
    -agent-key agent-keys.txt \
    -query-key query-keys.txt \
    -auth-mode static

# Override the default 30-second key refresh interval.
./bin/sepiida-server -p 9090 \
    -d "postgres://localhost:5432/sepiida?user=postgres&password=xxx" \
    -agent-key agent-keys.txt \
    -query-key query-keys.txt \
    -auth-mode static \
    -key-refresh 60
```

Self-hosted deployments use `static` mode. SaaS deployments use
`task-token` mode and configure a `SEPIIDA_TASK_TOKEN_SECRET` of at least
32 characters. Squid uses that secret to issue a write token for each task.

| Option | Description | Default |
| --- | --- | --- |
| `-p` | Listen port | `9090` |
| `-d` | PostgreSQL connection string | `postgres://localhost:5432/sepiida?user=postgres&password=postgres` |
| `-agent-key` | Agent key file | Required in `static` mode |
| `-query-key` | Query key file | Required |
| `-task-token-secret` | HMAC secret shared with Squid for per-task write tokens | Empty |
| `-auth-mode` | `static` or `task-token` | `task-token` |
| `-key-refresh` | Key refresh interval in seconds | `30` |

### 5. Start the Agent

```bash
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 \
    -id agent-001 \
    -w /mnt/data/output
```

| Option | Description | Default |
| --- | --- | --- |
| `-s` | Server URL | `http://localhost:9090` |
| `-key` | API key listed in `agent-keys.txt` | Required in static mode |
| `-id` | Agent ID | `agent-001` |
| `-i` | Push interval in seconds | `60` |
| `-w` | Parent directory containing UUID directories | `./output` |
| `-archive` | Object storage or local archive destination | Disabled |
| `-archive-key-id` | Storage access key ID; overrides environment variables | Read from the environment |
| `-archive-key-secret` | Storage secret key; overrides environment variables | Read from the environment |

### 6. Archive Workflow Outputs

After a workflow succeeds, the Agent can archive its outputs to object storage
or a local directory. Configure the destination with `-archive`.

```bash
# AWS S3
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive s3://my-bucket/prefix \
    -archive-key-id AKIA... \
    -archive-key-secret ...

# MinIO
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive http://minio.local:9000/my-bucket/prefix \
    -archive-key-id minioadmin \
    -archive-key-secret minioadmin

# Alibaba Cloud OSS
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive oss://cn-hangzhou/my-bucket/prefix \
    -archive-key-id LTAI... \
    -archive-key-secret ...

# Tencent Cloud COS with a virtual-hosted URL
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive https://mybucket-1250000000.cos.ap-guangzhou.myqcloud.com/prefix \
    -archive-key-id AKID... \
    -archive-key-secret ...

# Tencent Cloud COS short URL
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive cos://ap-guangzhou/mybucket-1250000000/prefix \
    -archive-key-id AKID... \
    -archive-key-secret ...

# Credentials can also be supplied through environment variables.
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive s3://my-bucket/prefix

# Local archive directory; no credentials are required.
./bin/sepiida-agent -s http://localhost:9090 \
    -key my-agent-key-001 -id agent-001 -w /mnt/data/output \
    -archive /mnt/archive/outputs
```

Supported destinations:

| URL format | Storage system | Example |
| --- | --- | --- |
| `s3://bucket/prefix` | AWS S3 | `s3://sepiida-archive/results` |
| `http://host:port/bucket/prefix` | MinIO over HTTP | `http://minio.local:9000/results` |
| `https://host:port/bucket/prefix` | MinIO over HTTPS | `https://minio.example.com/results` |
| `oss://region/bucket/prefix` | Alibaba Cloud OSS | `oss://cn-hangzhou/my-bucket/results` |
| `cos://region/bucket/prefix` | Tencent Cloud COS short URL | `cos://ap-guangzhou/mybucket-1250000000/results` |
| `https://<bucket>.cos.<region>.myqcloud.com/prefix` | Tencent Cloud COS virtual-hosted URL | `https://mybucket-1250000000.cos.ap-guangzhou.myqcloud.com/results` |
| Local path | Local filesystem | `/mnt/archive/outputs` |

Credential precedence:

| Storage system | CLI options | Environment fallback |
| --- | --- | --- |
| AWS S3 / MinIO | `-archive-key-id` and `-archive-key-secret` | `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` |
| MinIO | Same as above | `MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD` |
| Alibaba Cloud OSS | Same as above | `ALIBABA_CLOUD_ACCESS_KEY_ID` and `ALIBABA_CLOUD_ACCESS_KEY_SECRET` |
| Tencent Cloud COS | Same as above | `COS_SECRET_ID` and `COS_SECRET_KEY` |

Files archived after a workflow reaches `success`:

| File | Processing | Object key |
| --- | --- | --- |
| `workflow.log` | Uploaded directly | `{uuid}/workflow.log` |
| `outputs.json` | Uploaded directly | `{uuid}/outputs.json` |
| `inputs.json` | Uploaded directly | `{uuid}/inputs.json` |
| `.txt`, `.csv`, and `.tsv` files | Converted to separate Parquet files | `{uuid}/{filename}.parquet` |
| Other files such as BAM, BAI, and VCF.GZ | Flattened to their base filename | `{uuid}/{filename}` |

**Flattened archives:** Files referenced by `outputs.json` retain only their
base filename. Duplicate names receive a numeric suffix, for example
`result.bam` and `result_2.bam`.

**Dynamic Parquet schemas:** Each text file is converted independently. The
first row becomes the Parquet field names and subsequent rows become records.
Paths in `outputs.resolved.json` are updated to use the `.parquet`
extension.

**Idempotency:** The `.sepiida.json` file in each UUID directory records the
`Archived` state and prevents duplicate archives.

### 7. Query Results

Use a key from `query-keys.txt`:

```bash
# Get a workflow by UUID.
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:9090/api/v1/workflow?uuid=a1b2c3d4-e5f6-7890-abcd-ef1234567890"

# List workflows.
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:9090/api/v1/workflows"

# Get tasks for a workflow.
curl -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:9090/api/v1/workflow/tasks?id=20260428_094955_SingleWES"
```

## API

### Agent API

An Agent key or SaaS task token is required.

| Endpoint | Description |
| --- | --- |
| `POST /api/v1/progress` | Push progress data |
| `POST /api/v1/workflow/output` | Push `outputs.json` |
| `POST /api/v1/workflow/archive` | Report archive completion |

### Query API

A query key is required.

| Endpoint | Description |
| --- | --- |
| `GET /api/v1/workflow?uuid=xxx` | Get a workflow by UUID |
| `GET /api/v1/workflow?id=xxx` | Get a workflow by ID |
| `GET /api/v1/workflow/tasks?id=xxx` | Get workflow tasks |
| `GET /api/v1/workflows` | List workflows |
| `GET /api/v1/keys/status` | Show key status |
| `POST /api/v1/keys/reload` | Reload key files immediately |

### Public API

| Endpoint | Description |
| --- | --- |
| `GET /health` | Health check |

## Separate Write and Query Keys

| Key type | Capability | Configuration |
| --- | --- | --- |
| Agent key | Push progress data | `-agent-key` |
| Query key | Read workflow results | `-query-key` |

The separation ensures that Agents cannot query results and query clients
cannot push progress.

For backward compatibility, a query key line without a scope has unrestricted
query access. Production deployments can restrict a key to specific workflow
UUIDs or workflow IDs:

```text
# Unrestricted query access
my-query-key-001

# Restricted access; these keys cannot list workflows or manage keys.
my-scoped-query-key uuid=workflow-uuid-1,workflow-uuid-2
my-workflow-query-key workflow=workflow-id-1
```

## Dynamic Key Reloading

- The Server periodically checks key file modification times.
- Updated files are reloaded automatically.
- Changes become effective within the configured refresh interval.
- `POST /api/v1/keys/reload` forces an immediate reload and requires a query
  key.

```bash
curl -X POST -H "Authorization: Bearer my-query-key-001" \
    "http://localhost:9090/api/v1/keys/reload"
```

## Database Schema

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

## Dependencies

- Go 1.25+
- PostgreSQL
- [minio-go](https://github.com/minio/minio-go) for S3-compatible object
  storage
- [parquet-go](https://github.com/parquet-go/parquet-go) for writing Parquet
  output

## License

MIT
