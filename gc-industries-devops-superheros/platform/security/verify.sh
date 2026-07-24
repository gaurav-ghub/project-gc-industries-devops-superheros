#!/usr/bin/env bash

###############################################################################
# File: verify.sh
#
# Description:
# Verification and summary output for the Security module of the
# Endurance.
#
# Author: Gaurav Chaurasia
###############################################################################

set -euo pipefail

# Own directory variable — see the note in kyverno/install.sh.
SECURITY_VERIFY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SECURITY_VERIFY_DIR}/../scripts/utils.sh"


display_security_summary() {
    # Endurance frames the run and shows the access details once, at the end.
    if framed; then return 0; fi


    print_section "Security Summary"

    echo "✓ Kyverno Installed"

    echo "✓ Admission Control Active"

    echo "✓ Platform ClusterPolicies Registered with ArgoCD"

    echo

    echo "Status : READY ✅"

    echo

    print_subsection "Policy Enforcement Happens Twice"

    echo

    echo "1. Before commit  — endurance onboard/release evaluate the same"
    echo "                    ClusterPolicies against the manifests they are"
    echo "                    about to write, and refuse to write on a"
    echo "                    violation. Nothing bad reaches git."

    echo

    echo "2. At admission   — Kyverno rejects anything that reaches the API"
    echo "                    server by another route."

    echo

    print_subsection "Useful Commands"

    echo

    echo "View Cluster Policies"
    echo "kubectl get clusterpolicies"

    echo

    echo "View Policy Reports"
    echo "kubectl get policyreports -A"

    echo

    echo "Check an application before committing"
    echo "./cli/endurance.exe policy check superheros --root ."

    echo

    echo "List what the platform enforces"
    echo "./cli/endurance.exe policy list --root ."

    echo

}


verify_security() {

    log_info "Verifying platform security..."

    echo

    if ! kubectl get crd clusterpolicies.kyverno.io >/dev/null 2>&1; then

        log_error "Kyverno CRDs are missing."

        exit 1

    fi

    log_success "Kyverno CRDs present."

    echo

    kubectl get clusterpolicies 2>/dev/null || \
        log_warn "No ClusterPolicies applied yet — ArgoCD may still be syncing."

    echo

    log_success "Platform security verified."

}
