# Continuity Lab documentation

English is the default language for project documentation, source comments, configuration notes, user-facing UI copy, logs, and command help.

## English documentation

- [Project overview and quick start](../README.md)
- [Architecture and invariants](architecture.md)
- [Protocol notes](protocol.md)
- [Failure model](failure-model.md)
- [Operations](operations.md)
- [Implementation status and test evidence](../IMPLEMENTATION_STATUS.md)

### Architecture decisions

- [ADR 0001: Available pinned MinIO image](adr/0001-minio-image.md)
- [ADR 0002: Per-repository S3 CAS instead of a write leader](adr/0002-cas-without-leader.md)
- [ADR 0003: Delegate every ref to proc-receive](adr/0003-proc-receive-atomicity.md)

## Translations

- [Spanish: complete guide](es/guia-completa.md)

The translated guide is maintained as an additional resource. When wording differs, the English README, English technical documents, source code, and tests define the current behavior.

## Language policy

New canonical documentation and source comments must be written in English. Translations belong under a locale directory such as `docs/es/` and should link back to the English documentation index. Identifiers, protocol literals, CLI output consumed by automation, and API fields must not be translated.
