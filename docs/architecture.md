---
title: Havamal architecture
status: draft
date: 2026-10-12
related: [https://github.com/ouroboric-dev/yggdrasil/blob/main/docs/2-engineering/specs/2026-10-11-yggdrasil-v0-spec.md]
style: reference
---

# Havamal architecture

The skill library: vendored skills with `skills.lock` and licences, our own skills with their evals, and the tooling.

## Responsibilities

- Holds a pinned copy of every skill the factory loads, with `skills.lock`: upstream, path,
  commit, licence and per-file hashes (spec §5.1).
- Holds our own skills. Each ships with an eval that shows it measurably improves agent behaviour.
- Holds the tooling, including the drift check that fails when a copy differs from the lock.
- Includes only skills with redistributable licences from public upstreams, never
  organisation-internal skills.

## Depends on

- Nothing at build time. Gleipnir loads skills from it at a pinned version.

## Source

The v0 engineering spec, §5.1. When this page and the spec differ, the spec wins.
