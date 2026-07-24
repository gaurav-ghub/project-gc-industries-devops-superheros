###############################################################################
# File: install.sh
#
# Description:
# Public entry point for the Security module of the
# Endurance.
#
# Purpose:
# This script installs all policy and admission-control capabilities
# required by the platform.
#
# Responsibilities:
# - Install Kyverno
# - Register the platform's ClusterPolicies
# - Verify security components
#
# Philosophy:
# The platform owns policy.
# Customer applications consume policy.
#
# Author: Gaurav Chaurasia
###############################################################################

#!/usr/bin/env bash

set -euo pipefail

# Own directory variable, resolved BEFORE sourcing any child. These files are
# sourced into one shell, so a name shared with a child would be overwritten and
# the second `source` below would look in the wrong directory.
SECURITY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SECURITY_DIR}/../scripts/utils.sh"

source "${SECURITY_DIR}/kyverno/install.sh"
source "${SECURITY_DIR}/verify.sh"


install_security() {

    log_info "Installing Platform Security..."

    echo

    install_kyverno

    log_success "Platform Security installed."

    echo

    display_kyverno_policies

    display_security_summary

}

main() {

    install_security

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
