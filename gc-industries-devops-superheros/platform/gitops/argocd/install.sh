#!/usr/bin/env bash

set -euo pipefail

# This file is sourced by ../install.sh, not run as a subprocess, so it must not
# reuse the parent's GITOPS_DIR — assigning it here would overwrite the parent's
# value and send its next `source "${GITOPS_DIR}/verify.sh"` into this directory.
ARGOCD_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${ARGOCD_DIR}/../../scripts/utils.sh"


install_argocd() {

    log_info "Installing ArgoCD..."

    check_argocd_repository

    create_argocd_namespace

    install_argocd_chart

    verify_argocd_installation

    log_success "ArgoCD installed."

}

check_argocd_repository() {

    log_info "Checking ArgoCD Helm repository..."

    echo

    if helm repo list | grep -q "^argo"; then

        log_info "ArgoCD Helm repository already exists."

        echo

    else

        log_info "Adding ArgoCD Helm repository..."

        helm repo add argo https://argoproj.github.io/argo-helm

        echo

    fi

    log_info "Updating Helm repositories..."

    helm repo update

    echo

    helm repo list

    echo

    log_success "ArgoCD Helm repository is ready."

    echo

}


create_argocd_namespace() {

    log_info "Creating ArgoCD namespace..."

    echo

    if kubectl get namespace argocd >/dev/null 2>&1; then

        log_info "ArgoCD namespace already exists."

    else

        kubectl create namespace argocd

        log_success "ArgoCD namespace created."

    fi

    echo

}



# An installed release is upgraded, not skipped.
#
# This used to return early whenever `helm status argocd` succeeded, which made
# the module idempotent in the narrow sense and useless in the useful one: no
# change to values.yaml could ever reach a cluster that already had ArgoCD.
# Phase 5 puts the notification services, templates and triggers in that file,
# so "already installed" had to stop meaning "never reconfigured".
#
# Slack credentials, if any, come from values.slack.yaml — untracked, optional,
# and layered on top so the committed file never holds a token.
install_argocd_chart() {

    local -a values_args=( --values "${ARGOCD_DIR}/values.yaml" )

    if [[ -f "${ARGOCD_DIR}/values.slack.yaml" ]]; then

        log_info "Found values.slack.yaml — Slack notifications will be configured."

        values_args+=( --values "${ARGOCD_DIR}/values.slack.yaml" )

    else

        log_info "No values.slack.yaml — Slack is off; the in-cluster launchpad-sink webhook is still configured."

        log_info "See values.slack.yaml.example to enable Slack."

    fi

    echo

    if helm status argocd --namespace argocd >/dev/null 2>&1; then

        log_info "ArgoCD is already installed — upgrading to match values.yaml..."

    else

        log_info "Installing ArgoCD..."

    fi

    echo

    if helm upgrade \
        --install argocd \
        argo/argo-cd \
        --namespace argocd \
        "${values_args[@]}"
    then

        echo

        log_success "ArgoCD components installed."

    else

        log_error "Failed to install ArgoCD."

        exit 1

    fi

}


verify_argocd_installation() {

    log_info "Verifying ArgoCD installation..."

    echo

    verify_argocd_namespace

    verify_argocd_release

    wait_for_argocd_components

    verify_argocd_pods

    display_argocd_information

    log_success "ArgoCD installation verified."

}

verify_argocd_namespace() {

    log_info "Verifying ArgoCD namespace..."

    echo

    if kubectl get namespace argocd >/dev/null 2>&1; then

        log_success "ArgoCD namespace exists."
    

    else

        log_error "ArgoCD namespace does not exist."

        exit 1

    fi

    echo

}

verify_argocd_release() {

    log_info "Verifying ArgoCD Helm release..."

    echo

    if helm status argocd \
        --namespace argocd >/dev/null 2>&1; then

        log_success "ArgoCD Helm release is deployed."

    else

        log_error "ArgoCD Helm release is not deployed."

        exit 1

    fi

    echo

}

verify_argocd_pods() {

    log_info "Verifying ArgoCD pods..."

    echo

    kubectl get pods -n argocd

    echo

    # A one-shot init Job (argocd-redis-secret-init, shipped by the chart to
    # create the redis auth secret) legitimately ends as Completed/Succeeded —
    # that is the Job doing its job, not a failure. The old check was
    # `grep -vq Running`, which flagged that Completed pod and failed the
    # bootstrap even though every long-running component was healthy. Only pods
    # that are neither Running nor Completed are actually wrong.
    local unhealthy

    unhealthy="$(kubectl get pods \
        --namespace argocd \
        --no-headers \
        | awk '$3 != "Running" && $3 != "Completed" { print }')"

    if [[ -n "${unhealthy}" ]]; then

        echo "${unhealthy}"

        echo

        log_error "Some ArgoCD pods are not healthy."

        exit 1

    fi

    log_success "ArgoCD pods are running."

    echo

}

wait_for_argocd_components() {

    log_info "Waiting for ArgoCD workloads to roll out..."

    echo

    # Wait on the actual workloads (Deployments + the application-controller
    # StatefulSet), NOT on a pod label.
    #
    # The label app.kubernetes.io/instance=argocd also matches the one-shot
    # `argocd-redis-secret-init` Job pod, which runs to Completed and can never
    # satisfy --for=condition=Ready. The old `kubectl wait` therefore timed out
    # on it for the full 300s even when every long-running pod was 1/1 Running —
    # and "worked on the second run" only because the Job pod had been garbage
    # collected by then. `kubectl rollout status` tracks controllers and ignores
    # Jobs by construction, so it is the correct primitive here and the result no
    # longer depends on GC timing.
    local rc=0
    local kind name

    while read -r kind name; do

        [[ -z "${name}" ]] && continue

        log_info "  waiting for ${kind}/${name}..."

        if ! kubectl rollout status \
            --namespace argocd \
            "${kind}/${name}" \
            --timeout=300s
        then

            rc=1

        fi

    done < <(kubectl get deploy,statefulset \
                --namespace argocd \
                -o jsonpath='{range .items[*]}{.kind}{" "}{.metadata.name}{"\n"}{end}')

    echo

    if [[ "${rc}" -ne 0 ]]; then

        kubectl get pods -n argocd

        echo

        log_error "ArgoCD components failed to become Ready."

        exit 1

    fi

    log_success "All ArgoCD components are Ready."

    echo

}

display_argocd_information() {

    print_section "ArgoCD Information"

    display_argocd_namespace

    display_argocd_pods

    display_argocd_ui

    display_argocd_credentials

    display_argocd_useful_commands

    display_gitops_summary

    log_success "ArgoCD information displayed."

    echo

}

display_argocd_namespace() {

    print_subsection "ArgoCD Namespace"

    echo

    kubectl get namespace argocd

    echo

    log_success "ArgoCD namespace displayed."

    echo

}

display_argocd_pods() {

    print_subsection "ArgoCD Pods"

    echo

    kubectl get pods -n argocd

    echo

    log_success "ArgoCD pods displayed."

    echo

}


display_argocd_ui() {

    print_subsection "ArgoCD UI"

    echo

    echo "URL:"
    echo "https://localhost:8080"

    echo

    echo "Port Forward:"
    echo "kubectl port-forward svc/argocd-server -n argocd 8080:443"

    echo

    log_success "ArgoCD UI information displayed."

    echo

}



display_argocd_credentials() {

    print_subsection "ArgoCD Credentials"

    echo

    echo "Username:admin"
    echo "Password:${PASSWORD:-$(kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 --decode)}"


    echo

    log_success "ArgoCD credentials displayed."

    echo

}

display_argocd_useful_commands() {

    print_subsection "Useful Commands"

    echo

    echo "View ArgoCD Pods"
    echo "kubectl get pods -n argocd"

    echo

    echo "View ArgoCD Services"
    echo "kubectl get svc -n argocd"

    echo

    echo "View ArgoCD Applications"
    echo "kubectl get applications -n argocd"

    echo

    echo "ArgoCD UI"
    echo "kubectl port-forward svc/argocd-server -n argocd 8080:443"

    echo

    log_success "Useful commands displayed."

    echo

}

display_gitops_summary() {

    print_section "GitOps Summary"

    echo "✓ ArgoCD Installed"

    echo "✓ GitOps Control Plane Ready"

    echo "✓ Git Repository Integration Ready"

    echo "✓ Continuous Deployment Ready"

    echo

    echo "Status : READY ✅"

    echo

    echo "🎉 Welcome Onboard!"

    echo

    echo "Your platform is now ready to deploy applications"
    echo "using GitOps with ArgoCD."

    echo

    echo "Next Steps"
    echo "----------"

    echo "• Access the ArgoCD Dashboard"

    echo "• Login using the credentials above"

    echo "• Register your Git repository"

    echo "• Create your first ArgoCD Application"

    echo

    echo "Happy GitOps! 🚀"

    echo

}

main() {

    install_argocd

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi