#!/usr/bin/env bash
###############################################################################
# SUPERSEDED — prefer platform/monitoring/install.sh.
#
# ⚠ This script installs the Helm release under the name "monitoring", while the
#   monitoring module installs the same kube-prometheus-stack chart as
#   "prometheus". Running both leaves TWO copies of the stack in the monitoring
#   namespace, fighting over the same CRDs and ServiceMonitors. Use one or the
#   other, not both.
#
# Kept as a standalone escape hatch (it takes an environment argument and pins a
# chart version, which the module does not).
###############################################################################

set -euo pipefail

ENVIRONMENT="${1:-kind}"

NAMESPACE="monitoring"

# Resolve values files relative to this script, not to the caller's working
# directory. The paths used to be repo-root-relative, so the script only worked
# when invoked from exactly one directory and failed with a confusing
# "no such file or directory" from helm anywhere else.
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MONITORING_VALUES="${SCRIPTS_DIR}/../monitoring/values"

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
    -f "${MONITORING_VALUES}/base/prometheus-values.yaml" \
    -f "${MONITORING_VALUES}/${ENVIRONMENT}/prometheus-values.yaml"

echo
echo "Monitoring installation completed."