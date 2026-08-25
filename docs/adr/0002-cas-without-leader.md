# ADR 0002: Per-repository S3 CAS instead of a write leader

- Status: accepted
- Date: 2026-08-24

Every healthy node may prepare a push. The opaque ETag of `wal/head.json` serializes publication with a real `PUT If-Match`. A loser aborts local ref locks, catches up, and retries only if touched refs remain compatible. This keeps a preferred rendezvous node as an optimization rather than a correctness leader. Objects uploaded before a failed CAS remain harmless and are grace-period GC candidates.
