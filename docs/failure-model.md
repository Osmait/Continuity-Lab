# Failure model

The CAS replacement of `head.json` is the sole commit boundary. Before it, uploaded objects are invisible orphans. After it, the transaction is committed even when the client sees an error.

| Failure point | Authoritative state | Expected result |
| --- | --- | --- |
| Before payload upload | Unchanged | Push fails and remains invisible |
| After payload, before entry | Unchanged | Orphaned pack; later GC candidate |
| After entry, before CAS | Unchanged | Orphaned entry and pack |
| CAS returns 412 | Another write won | Abort locally, synchronize, validate, then retry or reject |
| After CAS, before local commit | Committed | Restart replays the WAL; client may see an uncertain failure |
| After local commit, before response | Committed | Client retry must converge to the same state |
| Gossip lost | Committed | Replicas repair themselves on the next read |
| Preferred node unavailable | No loss | Gateway uses the next healthy node |
| Local repository deleted | No loss | Materialize from snapshot plus WAL |
| MinIO temporarily unavailable | Truth cannot be verified | Writes fail; strict reads return 503 |
| Local pack corrupted | MinIO intact | Detect, evict, and rebuild |
| Remote pack checksum invalid | Source of truth corrupted | Refuse to serve, mark the repository corrupt, and return an explicit error |

## Failpoints

Lab mode exposes once-only markers for `after_payload_upload`, `before_entry_upload`, `after_entry_upload`, `before_head_cas`, `after_head_cas`, `before_local_ref_commit`, `after_local_ref_commit`, `before_http_success`, `drop_all_gossip`, and `corrupt_next_local_pack`. Set and clear them with `continuityctl failpoint`. They are disabled unless `CONTINUITY_LAB_MODE=true`.

`after_head_cas` intentionally reports an uncertain failure. Restarting or reading through that node replays the now-committed WAL entry. No recovery path attempts to roll back the authoritative pointer.
