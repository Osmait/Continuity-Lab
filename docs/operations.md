# Operations

## Start and inspect

```sh
make bootstrap
./bin/continuityctl cluster status
./bin/continuityctl repo create acme/demo
./bin/continuityctl repo inspect acme/demo
./bin/continuityctl repo refs acme/demo
./bin/continuityctl repo wal acme/demo
```

Node debug ports are 18081–18083. MinIO API and console bind only to `127.0.0.1:9000` and `127.0.0.1:9001`.

## Verify, compact, collect, and evict

```sh
./bin/continuityctl repo verify acme/demo
./bin/continuityctl repo verify acme/demo --all-nodes
./bin/continuityctl repo compact acme/demo
./bin/continuityctl repo gc acme/demo --dry-run
./bin/continuityctl repo evict acme/demo --node node-b
```

Compaction publishes a full non-thin reachable-object pack and metadata before CAS-installing the snapshot pointer. GC computes reachability from `head.json`, honors the grace period, and supports dry-run. Eviction confirms MinIO, locks the cache, tombstone-renames it, and never deletes object-store data.

## Failures

```sh
./bin/continuityctl failpoint set after_head_cas --node node-a --mode once
./bin/continuityctl failpoint clear after_head_cas --node node-a
make test-chaos
```

MinIO volume loss is data loss by definition. Node-volume loss is recoverable. Development credentials are deliberately public in Compose and must not be reused.

## Optional stale-read lab mode

Strict reads remain the default. Setting `CONTINUITY_ALLOW_STALE_READS=true` allows only upload-pack reads from a previously verified warm cache while MinIO is unavailable; receive-pack discovery and writes remain strict. The cache still passes refs checksum and Git connectivity verification before serving. Run `make test-stale` for the isolated demonstration.

## Web editor safety

The browser editor publishes direct commits to the selected branch. This lab has no authentication or authorization, so anyone who can reach the gateway can create commits through `POST /api/v1/edit/<repository>`. Keep all published ports bound to loopback and do not expose the service to an untrusted network.

Edits use an exact base commit and return HTTP 409 when the branch changes concurrently. Reload the file before retrying. Only regular UTF-8 files up to 4 MiB are accepted.

## Cleanup

```sh
make reset
```

This removes containers, named lab volumes, and test temporary directories.
