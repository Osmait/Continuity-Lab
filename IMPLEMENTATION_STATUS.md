# Continuity Lab — Implementation Status

This file tracks implementation gates. A phase is marked complete only after its real-process gate passes; mocks do not satisfy Git or MinIO gates.

## Current status

- [x] Phase 0 — Bootstrap and contracts
- [x] Phase 1 — One real Git node
- [x] Phase 2 — Authoritative WAL and CAS
- [x] Phase 3 — Materialization and disposable caches
- [x] Phase 4 — Three nodes and gateway
- [x] Phase 5 — UDP gossip
- [x] Phase 6 — Snapshots, GC, and chaos
- [x] Phase 7 — Documentation and finish
- [x] Phase 8 — React repository explorer

## Phase gates and evidence

### Phase 0 — Bootstrap and contracts

Deliverables: Go module, containers, Compose, validated config, object-store interface and S3 implementation, storage conformance, models, unit tests.

Gate (passed 2026-08-24):

```text
make bootstrap                              exit 0
make test-unit                              exit 0
./bin/continuityctl storage conformance     exit 0
```

Evidence: the pinned MinIO container started, all four Go services reported ready, and the conformance command verified `If-None-Match`, opaque ETags, failed and successful `If-Match`, conditional GET/304 semantics, and immutable-write behavior against real MinIO.

### Phase 1 — One real Git node

Deliverables: repository creation, Smart HTTP through `git-http-backend`, real clone/fetch/push, installed hooks.

Gate (passed 2026-08-24): a stock Git 2.55 client pushed to `node-a` through Smart HTTP, then a fresh clone matched `rev-parse HEAD` and passed `git fsck --full`. Real `git-http-backend`, `pre-receive`, `proc-receive`, and `post-receive` ran in the container.

### Phase 2 — Authoritative WAL and CAS

Deliverables: durable normalized push pack, strict proc-receive pkt-line protocol, prepare/CAS/commit, immutable WAL, concurrency tests.

Gate (passed 2026-08-24): the proc-receive hook produced a normalized pack, uploaded immutable WAL entries, held `git update-ref --stdin` across prepare/CAS/commit, and committed by real MinIO `If-Match`. Six simultaneous direct-node same-ref pushes from one old OID produced exactly one winner. Twenty simultaneous direct-node pushes to independent branches all committed; authoritative sequence advanced from 1 to 21 with 21 refs.

### Phase 3 — Materialization and disposable caches

Deliverables: replay, local state, eviction, strong reads, corruption recovery.

Gate (passed 2026-08-24): `acme/demo` was compacted at sequence 2, `node-b` was evicted, and a direct stock-Git clone from port 18082 rematerialized from the snapshot and passed `git fsck --full` with the expected HEAD. A local pack on node-b was then overwritten in place; the next direct clone detected failed connectivity, rebuilt from MinIO, and passed `git fsck --full`.

### Phase 4 — Three nodes and gateway

Deliverables: rendezvous routing, health-aware proxying, isolated node volumes, write fallback and read convergence.

Gate (passed 2026-08-24): all three isolated node volumes cloned the same sequence-2 repository and passed `git fsck`. Rendezvous preferred `node-b` for `acme/demo`; after stopping node-b and waiting for health detection, a gateway push committed through the fallback at sequence 3. After restarting node-b, direct clones through ports 18081, 18082, and 18083 all converged to the new HEAD.

### Phase 5 — UDP gossip

Deliverables: authenticated UDP notification/catch-up, coalescing, metrics, loss failpoint.

Gate (passed 2026-08-24): after all replicas cached a repository at sequence 1, a direct node-a push caused node-b and node-c local state to reach sequence 2 without a Git read. With the once-only `drop_all_gossip` failpoint, node-b remained at sequence 1 after the next push, then a normal `git fetch` performed conditional-GET catch-up and reached sequence 2 with the expected OID.

### Phase 6 — Snapshots, GC, and chaos

Deliverables: compact/restore, reachability GC with dry-run, failpoints, chaos suite.

Gate (passed 2026-08-24): manual and demo compaction published a full snapshot by CAS; eviction rematerialized from snapshot and passed `git fsck --full`; GC dry-run reported retained/reclaimable objects without deleting. `make test-chaos` proved an uploaded pre-CAS entry remained invisible, an `after_head_cas` uncertain failure recovered as committed after restart, and strict reads failed while MinIO was stopped before converging after restart.

### Phase 7 — Documentation and finish

Deliverables: README, architecture and failure docs, ADRs, reproducible demo, troubleshooting.

Remaining completion work (reopened 2026-08-24):

- [x] Operational `CONTINUITY_ALLOW_STALE_READS=true` override with unit and real-process tests
- [x] `continuityctl repo verify --all-nodes`
- [x] Event-backed metrics for CAS, materialization, replay, gossip, strong reads, compaction, and invariant failures
- [x] Full required failpoint/chaos matrix
- [x] Quarantine-pack/snapshot integration coverage and basic non-promotional benchmark
- [x] Structured operation fields and linux/amd64 + linux/arm64 build/manifest validation

Completion-gap gate (passed from a clean state after reopening):

```text
make reset && make bootstrap && make test-all && make demo   exit 0
```

Additional evidence: real-process stale-read override test passed while writes remained strict; all-node verification returned success for node-a/node-b/node-c; hook-emitted CAS/materialization/replay/gossip metrics became nonzero in node Prometheus output; the expanded chaos suite passed all required pre-/post-CAS failpoints, gossip loss, local corruption, preferred-node fallback, compaction race, and MinIO outage; the multi-architecture check compiled every binary for linux/amd64 and linux/arm64 and verified both base manifests.

Original clean-state baseline:

```text
make reset       exit 0
make bootstrap   exit 0
make test-all    exit 0
make demo        exit 0
```

`make test-all` included `go vet`, unit tests, `go test -race`, real MinIO/Git integration tests, the 100-commit E2E suite, 20/20 independent-ref concurrency, exactly 1/20 same-ref winners, and pre-/post-CAS chaos recovery. The demo finished with real Smart HTTP pushes, all three replicas, WAL inspection, snapshot compaction, eviction, rematerialization, and `git fsck --full`.

### Phase 8 — React repository explorer

Deliverables: Vercel-inspired React/TypeScript Continuity Git interface at `/`, repository creation, branch/tag selection, tree and blob browsing, README preview, commit history/detail, and a lazy-loaded CodeMirror editor for creating and editing UTF-8 files; plus a distinct read-only Continuity Admin console at `/admin` for real node health, aggregate authoritative WAL history, and logical MinIO records. Web edits create real Git commits and publish them through Smart HTTP, hooks, WAL, and CAS with optimistic base-OID conflict detection.

Gate (passed with real Compose services):

```text
npm run build --prefix web    exit 0
go test ./internal/...        exit 0
./scripts/test-ui.sh          exit 0
make test-all                 exit 0
```

The real-process UI test created and pushed a repository through Smart HTTP, loaded the embedded Git and Admin SPA shells and their deep links, then verified repository listing, refs, root/subdirectory trees, text blob content, commit history, and commit changes through the gateway and a synchronized Git node. It also created and edited a file through the web-edit API, verified two additional authoritative WAL entries and a clean clone, and confirmed that a stale base OID returns HTTP 409.

## Notes

- Implementation is original and educational, based only on publicly documented Git, S3/MinIO, and distributed-systems behavior.
- Continuity Lab is not affiliated with Cursor.
