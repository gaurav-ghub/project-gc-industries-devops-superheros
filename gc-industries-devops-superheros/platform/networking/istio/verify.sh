
verify_istio_installation() {

    log_info "Verifying Istio installation..."

    echo

    verify_istio_namespace
    verify_istiod
    verify_ingress_gateway

    echo

    log_success "Istio installation verified."

}

verify_istio_namespace() {

    log_info "Checking istio-system namespace..."

    echo

    if kubectl get namespace istio-system >/dev/null 2>&1; then

        kubectl get namespace istio-system

        echo

        log_success "Namespace verified: istio-system"

    else

        log_error "Namespace 'istio-system' not found."

        exit 1

    fi

}

verify_istiod() {

    log_info "Checking istiod..."

    echo

    kubectl get deployment istiod \
        -n istio-system

    echo

    if kubectl rollout status deployment/istiod \
        -n istio-system \
        --timeout=60s
    then

        log_success "istiod verification passed."

    else

        log_error "istiod verification failed."

        exit 1

    fi

}


verify_ingress_gateway() {

    log_info "Checking ingress gateway..."

    echo

    kubectl get deployment istio-ingressgateway \
        -n istio-system

    echo

    if kubectl rollout status deployment/istio-ingressgateway \
        -n istio-system \
        --timeout=60s
    then

        log_success "Ingress Gateway verification passed."

    else

        log_error "Ingress Gateway verification failed."

        exit 1

    fi

}