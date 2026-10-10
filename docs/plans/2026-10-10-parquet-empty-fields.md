# Preserve empty TSV fields and recover archived Parquet

**Goal:** Preserve every valid report row during Sepiida archive conversion and recover the two affected SaaS task datasets.

**Architecture:** Keep tab delimiters intact and validate every record against the header. Parsing or writing failures reject the entire conversion so the existing archiver preserves the original text and references it in the manifest. Recovery uses the original COS reports, immutable replacement objects and an attempt-scoped Parquet catalogue.

**Tech Stack:** Go encoding/csv, parquet-go, Octopus results-parquet maintenance command, Tencent COS.

1. Add regression coverage for leading, adjacent and trailing empty TSV fields, whitespace preservation, and malformed short/long records. Read the resulting Parquet to verify row count and column alignment.
2. Return parser errors instead of silently skipping records. Reject Parquet write failures instead of publishing partial data. Verify archive fallback retains the complete original report.
3. Run archiver and full Sepiida tests; build Linux Server and Agent binaries for the later CVM image update.
4. On the deployment host, verify both current task attempts and back up original manifests/dataset metadata. Rebuild affected Parquet from archived TXT through Octopus's controlled recovery command, preserving original objects.
5. Verify COS replacement objects, source/Parquet row counts, dataset metadata and the browser interpretation endpoint for both attempts. Record recovery evidence without credentials or signed URLs.

The user authorized source fixes and COS recovery. The CVM system image is updated separately by the user.
