#!/bin/sh
set -e

pcscd -d -a &
sleep 2

# Entrypoint script for HSM Secrets Operator
# Supports running manager, discovery, or agent binaries from the same container

case "$1" in
    "manager")
        shift
        exec /hsm-operator --mode=manager "$@"
        ;;
    "discovery")
        shift
        exec /hsm-operator --mode=discovery "$@"
        ;;
    "agent")
        shift
        exec /hsm-operator --mode=agent "$@"
        ;;
    "--mode="*)
        # Direct mode flag usage (preferred)
        exec /hsm-operator "$@"
        ;;
    *)
        # Default to manager for backward compatibility
        exec /hsm-operator --mode=manager "$@"
        ;;
esac