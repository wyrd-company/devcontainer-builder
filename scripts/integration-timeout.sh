#!/usr/bin/env bash
# relationships:
#   references: [ci-integration, docker-build-integration, docker-host-integration, docker-compose-integration]

# Shared Docker suites can approach Go's default 10-minute per-package limit.
# Keep the same explicit budget for direct scripts, Task verbs, and CI.
readonly integration_test_timeout=20m
