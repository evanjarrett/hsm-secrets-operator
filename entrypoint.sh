#!/bin/sh
set -e

pcscd &
sleep 2

# Entrypoint script for HSM Secrets Operator
# Supports running manager, discovery, or agent binaries from the same container

case "$1" in
    "manager")
        shift
        exec /manager "$@"
        ;;
    "discovery")
        shift
        exec /discovery "$@"
        ;;
    "agent")
        shift
        exec /agent "$@"
        ;;
    "--manager")
        shift
        exec /manager "$@"
        ;;
    "--discovery")
        shift
        exec /discovery "$@"
        ;;
    "--agent")
        shift
        exec /agent "$@"
        ;;
    *)
        # Default to manager for backward compatibility
        exec /manager "$@"
        ;;
esac