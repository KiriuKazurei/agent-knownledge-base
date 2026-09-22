---
name: architecture-analysis
description: Analyze a project architecture from an immutable local scan snapshot with evidence-linked facts, bounded inferences, and incremental change output.
compatibility: Knowledge Agent Hub stage 1; read-only local project snapshots
license: MIT
---

# Architecture analysis

Use the supplied `architecture.snapshot.v1` JSON as the only source of file
identity. Do not read outside the configured project root, execute project
commands, call an external Agent, or treat an unstated assumption as a fact.

Return `architecture.analysis.v1` JSON matching
`references/architecture-analysis.schema.json`.

Facts are directly supported by snapshot metadata or a cited path and line
range. Every fact and inference cites one or more evidence IDs. An inference
is a bounded interpretation of facts and includes confidence from 0 through 1;
state uncertainty in the statement when the snapshot cannot establish it.
Dependency names and versions, entry points, container files, Git revision,
dirty paths, and language coverage are facts only when present in the snapshot.

For an incremental result, compare the current snapshot to `baseSnapshotId` by
normalized path and content hash. Report added, changed, deleted, and
unchanged paths separately. A deleted path may be cited only by the base
snapshot, while current facts and evidence must cite the current snapshot.

Keep output bounded and deterministic: stable IDs, sorted path arrays, no source
file bodies, and no credentials or secret paths. Unknown stacks remain explicit
with `unknown: true` and a reason in the evidence-backed statement.

## Output contract

The machine-readable contract is retained at
`references/architecture-analysis.schema.json`. The API accepts it only when
the schema version is `kah.architecture.analysis.v1`, evidence paths exist in
the referenced immutable snapshot, line ranges are valid, content hashes
match, and all fact/inference references resolve to an evidence ID.

Later archify stages may attach derived diagrams or exports by snapshot ID;
they must not mutate this source snapshot or its evidence records.
