# ADR 0001: Available pinned MinIO image

- Status: accepted
- Date: 2026-08-24

## Context

The suggested `quay.io/minio/minio:RELEASE.2025-09-06T17-38-46Z` manifest does not exist in the public registry. A bootstrap using it cannot be reproducible.

## Decision

The default is the adjacent published multi-architecture release `quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z`. `MINIO_IMAGE` remains configurable. This changes no storage semantics; startup conformance verifies the conditional operations on which correctness depends.

If the image becomes unavailable, build the corresponding tag from <https://github.com/minio/minio> with its documented `make build` workflow, tag it locally, and set `MINIO_IMAGE` to that tag before running Compose.
