---
name: kah-knowledge-manage
description: Validate and submit KAH Knowledge Profile v1 drafts through the Manage MCP server. Use when an Agent needs to add or propose a revision of knowledge; do not use to publish or delete knowledge.
---

# KAH Knowledge Manage

1. Search first for duplicates and existing revisions.
2. Gather exact sources, source locators, and body citations. Do not invent citations.
3. Build a KAH v1 JSON candidate and call `knowledge_validate`.
4. Resolve blocking errors. For near duplicates choose `revision`, `supplement`, or `independent` through `duplicate_intent` and relations.
5. Call `knowledge_submit` with a fresh `idempotencyKey`. The result is a human-review draft, never a publication.
6. Call `knowledge_submission_get` to inspect review status.

Read [KAH v1 submission reference](references/kah-knowledge-v1.md) before submitting.
