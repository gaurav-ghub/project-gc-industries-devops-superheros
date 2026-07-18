#!/usr/bin/env bash

set -euo pipefail

GITOPS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${GITOPS_DIR}/../../scripts/utils.sh"


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



install_argocd_chart() {

    log_info "Installing ArgoCD..."

    echo

    if helm status argocd \
        --namespace argocd >/dev/null 2>&1; then

        log_warn "ArgoCD is already installed."

        echo

        return

    fi

    if helm upgrade \
        --install argocd \
        argo/argo-cd \
        --namespace argocd \
        --values "${GITOPS_DIR}/values.yaml"
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

    if kubectl get pods \
        --namespace argocd \
        --no-headers \
        | grep -vq "Running"
    then

        log_error "Some ArgoCD pods are not running."

        exit 1

    fi

    log_success "ArgoCD pods are running."

    echo

}

wait_for_argocd_components() {

    log_info "Waiting for ArgoCD components to become Ready..."

    echo

    if kubectl wait \
        --namespace argocd \
        --for=condition=Ready \
        pod \
        --all \
        --timeout=300s
    then

        echo

        log_success "All ArgoCD components are Ready."

    else

        echo

        log_error "ArgoCD components failed to become Ready."

        exit 1

    fi

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