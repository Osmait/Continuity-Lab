# ADR 0003: Delegate every ref to proc-receive

- Status: accepted
- Date: 2026-08-24

Each bare repository configures `receive.procReceiveRefs=refs/`. Continuity's strict protocol-v1 hook handles all commands in one internal transaction even when the client did not request atomic behavior. It holds a real `git update-ref --stdin` transaction in prepared state across object-store CAS and never asks receive-pack to fall through. This prevents receive-pack from applying refs a second time.
