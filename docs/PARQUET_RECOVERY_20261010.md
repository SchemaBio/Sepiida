# Parquet recovery — 2026-10-10

## Cause and source fix

The Agent used `encoding/csv.Reader.TrimLeadingSpace=true` for TSV reports. Consecutive tab delimiters representing empty annotation fields were consumed as whitespace. The default field-count check then rejected those records, and the converter silently skipped them.

The converter now preserves field contents and delimiters, rejects malformed records, and returns write failures instead of publishing partial Parquet. The existing archive fallback retains the complete original text and keeps the resolved manifest pointing to it.

Regression tests read generated Parquet and verify leading, adjacent and trailing empty fields, CRLF, whitespace, column alignment and row counts. Malformed short/long records and whole-report archive fallback are covered. `go test -p 1 ./...` passed; Linux amd64 Agent and Server builds passed. The initial parallel Windows test run encountered a temporary executable file lock; the serial run used a separate build directory and completed successfully.

## Recovered tasks

| Task | Execution attempt |
| --- | --- |
| `fc5ebe5d-1a87-4901-864d-6b75428ef5ae` | `9e0695e8-a77c-49b7-86c5-14a0d4536d55` |
| `3987e24e-4eb6-4ce0-916e-198771b04bda` | `a4a17ea3-71fb-486c-b059-0ea17326d352` |

Both attempts have these validated result counts:

| Table | Previous Parquet rows | Restored/current rows |
| --- | ---: | ---: |
| SNV/Indel | 55,041 | 55,459 |
| CNV segment | 2 | 64,732 |
| MEI | 39 | 40 |
| CNV exon | Original text only | 18,412 |
| ROH | Original header-only text | 0 |
| STR | 37 | 37 |
| Mitochondrial variants | 302 | 302 |

Octopus's existing `results-parquet --backfill --repair-table ... --execute` maintenance command rebuilt SNV/Indel, CNV segment and MEI from the full archived TXT reports. Its backfill also made the existing CNV exon and empty ROH reports available as Parquet. STR and mitochondrial objects already had matching row counts.

The bucket is `schemabio-user-1327430028`. Replacement objects are under the original attempt prefix:

```text
organizations/<org_uuid>/workflows/<task_uuid>/attempts/<attempt_id>/results-parquet-v1/
```

The attempt-scoped `results.parquet.catalog.json` selects the replacement objects and records their original report sources, SHA-256 hashes and row counts. Original report/Parquet objects and `outputs.resolved.json` remain available. Dataset metadata was refreshed through Octopus's maintenance command and browser dataset API.

All seven browser dataset endpoints returned HTTP 200 for both tasks (14 checks). Each downloaded COS Parquet object's SHA-256 matched the dataset metadata, and expected report counts matched query-service row counts. No workflow was rerun and no billing operation was requested.

Recovery backups, object inventories, command logs and verification evidence are on the deployment host at:

```text
/home/ubuntu/schema/backups/parquet-recovery-20261010/
```

## Agent for the next CVM image

The fixed source and Linux amd64 binaries are available locally and on the deployment host:

```text
Sepiida/bin/parquet-fix-20261010/sepiida-agent
Sepiida/bin/parquet-fix-20261010/sepiida-server
```

Agent SHA-256: `cf71ff4533d85bcdb322e085d62f83837bb0fb2b4f013233270258b3f058c567`.

Server SHA-256: `13b45dcb0eb5afa1efe57f6a460c8212cb25e2a70cd272a75fb0739c2399f03a`.

When preparing the new CVM image, install the fixed Agent at `/usr/local/bin/sepiida-agent`, make it executable and verify its SHA-256. The user updates the CVM image separately; this recovery does not replace the image or restart running services.
