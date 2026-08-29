# KAH Knowledge Profile v1 submission rules

Use core type and required section IDs exactly as specified by the Read Skill reference.

Provide at least one `https://` source or a `kah://knowledge/...?...revision=n` source. Every source must be cited in a section with `[^source-id]`. External HTTPS sources are snapshot-checked by the server; a failed snapshot produces a draft flagged `source-unverified` and blocks publication.

Evidence relations `supports`, `contradicts`, `derived_from`, and `supersedes` require `target_revision`. Allowed relations are `broader`, `part_of`, `related`, `depends_on`, `applies_to`, `example_of`, `supports`, `contradicts`, `derived_from`, `supersedes`, and `translation_of`.
