# ADR-0008: AGPL-3.0

**Status:** accepted (LICENSE at repository root)

## Context

The user wanted a restrictive copyleft license and asked whether anything speaks against GPLv3.

## Decision

AGPL-3.0-only for the whole repository. Because Fundus can run as a network service (Docker image, home server), AGPL closes the service loophole that GPLv3 leaves open.

## Consequences

- Go and Flutter dependencies are MIT/BSD/Apache and compatible.
- The FSF considers Apple's App Store terms incompatible with (A)GPL. Should an iOS client be published there, the client packages can be relicensed (or granted an App Store exception) while the number of contributors is small; contributors would need to agree to that relicensing.
- Companies that avoid AGPL will not contribute; acceptable for a personal open-source project.
