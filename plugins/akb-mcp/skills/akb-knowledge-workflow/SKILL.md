---
name: akb-knowledge-workflow
description: Use Agent Knowledge Base MCP to search, compare, validate, submit, and review evidence-backed KAH knowledge from project documents. Use when managing scoped knowledge; Agent approval requires confidence above 95 percent.
---

# Agent Knowledge Base workflow

Use this skill whenever the task involves finding, understanding, deduplicating, or organizing project knowledge in Agent Knowledge Base (AKB/KAH).

## Safety and scope

- Treat KAH stable revisions as the authoritative knowledge surface. Do not infer current truth from an old plan, an unverified source, or a previous assistant response.
- Use the `akb-read` MCP server for `knowledge_search`, `knowledge_get`, and read resources.
- Use the `akb-manage` MCP server for submission queue management, `knowledge_validate`, `knowledge_submit`, `knowledge_submission_get`, `knowledge_compare`, and `knowledge_review`.
- `knowledge_submit` creates a review-gated draft. It does not approve or publish by itself. `knowledge_review` is the only Agent review action: the server publishes an approval only when the supplied evidence-backed confidence is strictly greater than `0.95`.
- A confidence of `0.95` or lower is recorded as `needs_human` and remains `pending_review`. Do not retry the same immutable submission with a higher confidence; gather additional independent evidence and submit a new revision first.
- Rejection is allowed with a concrete reason. Approval is never a claim based only on the candidate payload; the server response must show `published` and `reviewStatus=published`.
- If MCP is unavailable or authorization fails, report the failure and do not bypass MCP by editing the KAH database directly.

## Read workflow

1. Search first with `knowledge_search`. Keep the query focused and pass `libraryIds` only when the user or context identifies a library.
2. For queue work, call `knowledge_submission_list` with `statuses=["pending_review"]`, then call `knowledge_submission_get` for the selected submission.
3. Treat search results as a directory. Follow the returned stable KAH resource URI with `knowledge_get` or the read resource operation before relying on a result.
4. Call `knowledge_compare` before reviewing. Use the previous revision by default, or pass `baseUri` / `baseSubmissionId` when comparing against a specific baseline. Inspect metadata, section, source, and relation changes.
5. Read the schema resource `kah://schema/kah-knowledge/v1` when the payload shape or section rules are uncertain.
6. Preserve evidence in the answer: cite the exact KAH URI, revision, section id, and source locator returned by AKB. Surface `stale`, `disputed`, and review-status signals instead of silently flattening them.

## Organizing project Markdown

When the user asks to turn local Markdown documents into knowledge:

1. Inspect the candidate files and classify each passage as durable fact, procedure, policy, claim, reference, historical acceptance evidence, or obsolete planning material.
2. Search AKB for duplicates and related stable revisions before drafting. Merge only claims supported by the source text; keep conflicts explicit.
3. Prefer small, independently reviewable knowledge entries. Use `reference` for a coherent overview, `procedure` for ordered operational steps, `policy` for constraints and scope, and `claim` for a single verifiable assertion.
4. Give every source a stable, resolvable resource URI and a precise locator. Cite each source in the body with its source id (for example `[^doc-1]`). Do not invent a web URL or a KAH URI for a local file.
5. Run `knowledge_validate` before `knowledge_submit`. Fix every blocking issue, check duplicate intent, and use a fresh idempotency key for each intentional submission.
6. After submission, call `knowledge_submission_get` and report the submission id, status, and review requirements. A `pending_review` result is an honest draft outcome, not publication.
7. When reviewing, call `knowledge_review` with `decision`, a numeric `confidence` in `0..1`, and a reason. Only a result with `decision=approve`, `confidence>0.95`, `published=true`, and `submission.reviewStatus=published` is an approved knowledge outcome.
8. If confidence is at or below `0.95`, use `decision=needs_human` or let the server convert an approval request to `needs_human`; then gather supplementary sources and submit a new immutable revision if further Agent work is requested.

## KAH v1 drafting checklist

- Include a meaningful `title`, `description`, `type`, `language`, `sections`, `sources`, and `tags`; use `primary_path` and `classifications` when taxonomy is useful.
- Use the required section ids for the selected type. Keep section bodies concise, factual, and independently understandable.
- Use the canonical wire keys exactly: `description` (not `summary`), section objects with `id`, `heading`, and `content` (not `title`/`body`), and source objects with `id`, `resource`, `locator`, and, for imported documents, `snapshot.content_hash` (not `uri` or a top-level `content_hash`). For example:

  ```json
  {
    "schema": "kah-knowledge/v1",
    "type": "reference",
    "title": "A concise title",
    "description": "A concise description",
    "language": "zh-CN",
    "sections": [{"id": "overview", "heading": "概览", "content": "Evidence-backed text. [^source-1]"}],
    "sources": [{"id": "source-1", "resource": "kah://document/<uuid>", "locator": {"section": "README.md#heading"}, "snapshot": {"content_hash": "<hash>"}}],
    "tags": ["topic"]
  }
  ```
- Use the exact token-scoped library UUID supplied by the caller or runtime context; `default` and `source-unverified` are not library aliases.
- Do not invent `status`, `confidence`, or `scope` fields: workflow state and review status are managed by the server, while scope is expressed through the selected library and optional taxonomy fields.
- New submissions are review-gated by the server; do not try to mark a candidate as published in its payload.
- Put uncertainty, version boundaries, and unresolved conflicts in the knowledge body or review notes rather than hiding them.
- Do not call desktop-only HTTP review routes, edit the KAH database, delete knowledge, or mutate a source document. Agent review is limited to the scoped `knowledge_review` MCP operation and its server-enforced confidence gate.

## Local-document provenance

Use the document URI exposed by `resources/list` or `resources/templates/list` (for example `kah://document/{documentId}`). The MCP server resolves it, enforces library scope, requires a `ready` document, and verifies `sources[].snapshot.content_hash` against the current indexed document. If any of those checks fail, stop and repair the provenance instead of weakening it.
