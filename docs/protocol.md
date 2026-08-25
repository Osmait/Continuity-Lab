# Protocol notes

## Smart HTTP

Nodes bridge requests to the installed `git-http-backend` CGI with `GIT_PROJECT_ROOT`, rewritten repo-ID `PATH_INFO`, request metadata, `HTTP_GIT_PROTOCOL`, and Continuity push identity. Public names never become filesystem paths.

## Push transaction

1. `pre-receive` validates all commands, enumerates quarantine objects with real Git plumbing, creates and validates a normalized non-thin pack, uploads it with `If-None-Match: *`, and fsync/renames pending metadata.
2. `proc-receive` performs strict pkt-line v1 negotiation. It validates ordered commands against pending metadata and treats every command group atomically.
3. `git update-ref --stdin` replies `start: ok` and `prepare: ok`; locks remain held while the hook uploads the immutable candidate entry and conditionally replaces `head.json`.
4. A 412/409 causes `abort: ok`, strong catch-up, compatibility validation, and jittered retry. A successful CAS causes `commit: ok`.
5. `post-receive` verifies local refs, sends authenticated UDP invalidations, and removes pending metadata. It is not part of correctness.

## Authoritative read

The conditional GET of `head.json` is the read linearization point. HTTP 304 permits a verified local cache. HTTP 200 triggers replay or rebuild. Store unavailability returns 503 while stale reads remain disabled.

## Browser edit transaction

`POST /api/v1/edit/<repository>` accepts one UTF-8 file creation or edit against a full branch ref and an exact base commit OID. Tags are read-only. The gateway validates the repository name, branch, path, content size, commit message, and author identity before invoking Git.

The gateway creates a temporary bare clone through its own Smart HTTP read path, verifies that the selected branch still equals the submitted base OID, and builds the commit with `read-tree`, `hash-object`, `update-index`, `write-tree`, and `commit-tree`. It then executes a normal `git push` through the gateway. The push therefore runs the same `git-http-backend`, quarantine, hooks, immutable pack upload, WAL publication, and `head.json` CAS as any external client.

A stale base OID returns HTTP 409. The receive path performs the final concurrency check, so a branch change between the preliminary check and push is also rejected. Temporary editor repositories are removed after each request. The editor supports regular UTF-8 files up to 4 MiB; it does not edit symlinks, binary files, tags, or `.git` paths.

## Object safety

ETags remain opaque including HTTP-required quotes. Pack key, size, SHA-256, WAL parent key, sequence, repository ID, OIDs, and refs are validated. Packs enter Git only through `index-pack --stdin --strict`; no archive paths are extracted.
