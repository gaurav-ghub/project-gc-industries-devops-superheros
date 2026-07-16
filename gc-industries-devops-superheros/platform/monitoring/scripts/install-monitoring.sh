#!/usr/bin/env bash

set -euo pipefail

ENVIRONMENT="${1:-kind}"

echo "======================================="
echo "GC Industries Monitoring Installer"
echo "Environment : ${ENVIRONMENT}"
echo "======================================="

case "${ENVIRONMENT}" in
  kind)
    echo "Installing monitoring stack for Kind..."
    ;;
  eks)
    echo "Installing monitoring stack for Amazon EKS..."
    ;;
  *)
    echo "Unsupported environment: ${ENVIRONMENT}"
    exit 1
    ;;
esac