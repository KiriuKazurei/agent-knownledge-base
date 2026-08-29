---
name: kah-knowledge-read
description: Search and cite KAH knowledge through the Read MCP server. Use when an Agent needs evidence-backed knowledge from a KAH library; do not use to submit or edit knowledge.
---

# KAH Knowledge Read

1. Call `knowledge_search` with the task and narrow filters when available.
2. Treat the returned directory as an index. Select relevant URIs before reading content.
3. Call `knowledge_get` with only needed section IDs; use `resources/read` for canonical exported Markdown.
4. Cite the knowledge URI, exact revision, section ID, and cited source locator. Surface stale or disputed warnings.

Read [KAH v1 schema](references/kah-knowledge-v1.md) before making an unsupported format assumption.
