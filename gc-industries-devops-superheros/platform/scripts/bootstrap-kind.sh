#!/usr/bin/env bash

###############################################################################
# File: bootstrap-kind.sh
#
# Description:
# Bootstraps the complete Endurance platform on a local Kind Kubernetes
# cluster, in one shell, module by module.
#
# Responsibilities:
#   - Validate platform prerequisites and create the Kind cluster (cluster.sh)
#   - Install networking (Istio)
#   - Install monitoring (kube-prometheus-stack)
#   - Install AI alert enrichment
#   - Install GitOps (ArgoCD)
#   - Install security (Kyverno)
#   - Open the access layer (Kiali, ingress Gateway, platform routes)
#
# This script is platform-operator plumbing, not the developer-facing surface.
# `endurance bootstrap` runs the same modules as separate subprocesses and
# frames their output through the Go renderer — that is the front door. This
# file stays because an operator debugging one module should be able to run the
# chain without the CLI, and because the CLI must never become the only place
# the platform knows how to install itself.
#
# Author: Gaurav Chaurasia
###############################################################################


SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SCRIPT_DIR}/utils.sh"

# Cluster creation lives in cluster.sh so that `endurance bootstrap` can run it
# as its own step. One definition, two callers.
source "${SCRIPT_DIR}/cluster.sh"

source "${PROJECT_ROOT}/platform/networking/install.sh"
source "${PROJECT_ROOT}/platform/monitoring/install.sh"
source "${PROJECT_ROOT}/platform/ai/install.sh"
source "${PROJECT_ROOT}/platform/gitops/install.sh"
source "${PROJECT_ROOT}/platform/security/install.sh"
source "${PROJECT_ROOT}/platform/access/install.sh"


main() {

    print_banner

    validate_local_environment

    bootstrap_cluster

    install_networking

    install_monitoring

    # AI alert enrichment runs after monitoring: it deploys into the monitoring
    # namespace next to Alertmanager, and the Alertmanager route that feeds it
    # ships in the monitoring module's values.
    install_ai

    install_gitops

    # Security runs after GitOps: it registers the ClusterPolicies as an ArgoCD
    # Application, so ArgoCD has to exist first.
    install_security

    # Access runs last: every route it applies points at a Service an earlier
    # module created, and a route to a Service that does not exist yet is a 503
    # the first time anybody looks.
    install_access

    log_success "Local platform bootstrap completed successfully."

}


main
