###############################################################################
# File: install.sh
#
# Description:
# Public entry point for the GitOps module of the
# Endurance.
#
# Purpose:
# This script installs all GitOps capabilities required
# by the platform.
#
# Responsibilities:
# - Install GitOps Tools
# - Verify GitOps Components
# - Prepare platform GitOps for customer applications
#
# Philosophy:
# The platform owns GitOps.
# Customer applications consume GitOps capabilities.
#
# Author: Gaurav Chaurasia
###############################################################################

#!/usr/bin/env bash

set -euo pipefail

GITOPS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${GITOPS_DIR}/../scripts/utils.sh"

source "${GITOPS_DIR}/argocd/install.sh"
source "${GITOPS_DIR}/verify.sh"



install_gitops() {

    log_info "Installing Platform GitOps..."

    echo

    install_argocd

    log_success "Platform GitOps installed."

    echo

    display_gitops_summary

}

main() {

    install_gitops

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
