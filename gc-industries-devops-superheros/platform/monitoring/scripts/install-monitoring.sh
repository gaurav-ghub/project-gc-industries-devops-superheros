#!/usr/bin/env bash

set -euo pipefail

ENVIRONMENT="${1:-kind}"

NAMESPACE="monitoring"

echo "========================================"
echo "GC Industries Monitoring Installer"
echo "Environment: ${ENVIRONMENT}"
echo "========================================"

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts

helm repo update

helm upgrade --install monitoring \
    prometheus-community/kube-prometheus-stack \
    --namespace "${NAMESPACE}" \
    --create-namespace \
    --version 77.11.0 \
    -f platform/monitoring/values/base/prometheus-values.yaml \
    -f "platform/monitoring/values/${ENVIRONMENT}/prometheus-values.yaml"

echo
echo "Monitoring installation completed."