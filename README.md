# GC Industries – DevOps SuperHero Shop 🤖🏅⭐

A modern, production-style microservices application that showcases advanced DevOps, GitOps, and Service Mesh practices using Kubernetes (AWS EKS), Istio, Argo CD, and GitHub Actions.

![Architecture Diagram](gc-industries-devops-superheros/docs/pictures/arch_diagram.png)
*SuperHeroes Shop – System Architecture*

---

## 📌 Inspiration & Acknowledgements
This project draws inspiration from:
- **Instana RoboShop** (polyglot microservices design)
- **Istio Bookinfo** (service mesh, traffic management, canary deployments)

> All code, CI/CD, Helm, and GitOps automation in this repo is original and independently implemented.

---

## 🚀 What Makes This Project Unique?
- Custom polyglot microservices, not a clone or fork
- End-to-end CI/CD with GitHub Actions
- Pipeline-driven GitOps with Argo CD
- Real canary deployments (multi-version, v1/v2/v3)
- Phased evolution: local → Kubernetes → AWS EKS
- Istio traffic control preserved during automation

This is a real-world platform engineering example, not just a demo.

---

## 🧩 Microservices & Technology Stack

| Service   | Responsibility         | Language / Framework   |
|-----------|-----------------------|------------------------|
| Frontend  | Web UI                | JavaScript (React)     |
| Catalog   | Product catalog (v1/v2/v3) | Go (Golang)      |
| Inventory | Stock management      | Python (FastAPI)       |
| Orders    | Order processing      | Node.js (Express)      |
| Payment   | Payment processing    | .NET (C#)              |

⚠️ *A Java-based payment service is included for reference only and is not part of the active deployment or CI/CD.*

---

## 🖼️ Application UI Screenshots

![Home Page](gc-industries-devops-superheros/docs/pictures/superhero_shop_app_home.png)
*SuperHeroes Shop Home Page*

![Catalog v1](gc-industries-devops-superheros/docs/pictures/superhero_shop_app_catalog_v1.png)
*Catalog Service v1*

![Catalog v2](gc-industries-devops-superheros/docs/pictures/superhero_shop_app_catalog_v2.png)
*Catalog Service v2 with ratings*

![Catalog v3](gc-industries-devops-superheros/docs/pictures/superhero_shop_app_catalog_v3.png)
*Catalog Service v3 with new products and ratings*

![Payment Page](gc-industries-devops-superheros/docs/pictures/superhero_shop_app_payment.png)
*Payment Service UI*

![Order Page](gc-industries-devops-superheros/docs/pictures/superhero_shop_app_order.png)
*Order Service UI*



---

## 🧱 Project Evolution: Phased Approach

### Phase 1: Application & Containers (Local)
- Designed and implemented all microservices
- Dockerized each service
- Validated the system locally with Docker Compose

🎯 *Goal: Validate design and containerization*

---

### Phase 2: Kubernetes, Istio & GitOps (Kind)
- Migrated to Kubernetes (Kind)
- Installed Istio (service mesh, traffic control)
- Installed Argo CD (GitOps)
- Introduced Helm charts
- Implemented Istio canary traffic shifting for Catalog
- Enabled observability: Kiali, Prometheus, Grafana

🎯 *Goal: Kubernetes-native deployments and service mesh patterns*


---

### Phase 3: Cloud & Production-Style GitOps (AWS EKS)
- Migrated to AWS EKS
- GitHub Actions CI for each microservice (path-based triggers, immutable images)
- Pipeline-driven GitOps: CI builds, CD commits desired state to Git, Argo CD reconciles
- Preserved Istio canary deployments during rollouts
- Enabled safe rollbacks via Git

🎯 *Goal: Simulate enterprise CI/CD and GitOps workflows*

![Git Bash: Cluster Creation](gc-industries-devops-superheros/docs/pictures/superheros_eks_cluster_command.png)
*Git Bash CLI: EKS cluster creation command*

![EKS Cluster](gc-industries-devops-superheros/docs/pictures/superheros_eks_cluster.png)
*AWS EKS cluster running the application*

![Istio Install](gc-industries-devops-superheros/docs/pictures/istio_install.png)
*Istio installation on local Kind cluster*

![ArgoCD Install](gc-industries-devops-superheros/docs/pictures/argocd_install.png)
*Argo CD installation and setup*

![Git Bash: ArgoCD App](gc-industries-devops-superheros/docs/pictures/argocd_app_1.png)
*Git Bash CLI: ArgoCD application sync*

![Git Bash: ArgoCD App 2](gc-industries-devops-superheros/docs/pictures/argocd_app_2.png)
*Git Bash CLI: ArgoCD application status*

![Code Push Pipeline](gc-industries-devops-superheros/docs/pictures/code_push.png)
*CI/CD pipeline in action: code push triggers build and deployment*

![Kiali Mesh Overview](gc-industries-devops-superheros/docs/pictures/kiali_1.png)
*Kiali: Service mesh topology overview*

![Kiali Traffic Animation](gc-industries-devops-superheros/docs/pictures/Kiali_2.png)
*Kiali: Real-time traffic animation*

![Kiali Graph](gc-industries-devops-superheros/docs/pictures/kiali_3.png)
*Kiali: Detailed service graph*

![Kiali Metrics](gc-industries-devops-superheros/docs/pictures/kiali_4.png)
*Kiali: Service metrics and health*

![Kiali Tracing](gc-industries-devops-superheros/docs/pictures/kiali_5.png)
*Kiali: Distributed tracing*

![Istio Gateway](gc-industries-devops-superheros/docs/pictures/istio_ingress_gateway_LB.png)
*Istio Ingress Gateway with AWS LoadBalancer*
---

## 🔁 GitOps Strategy: Pipeline-Driven
- CI pipelines build images only
- CD pipelines commit desired state to Git
- Argo CD is the only component that deploys to Kubernetes
- No pipelines run `kubectl apply` or `helm install`

**Benefits:**
- Clear audit trail
- Simple rollbacks (`git revert`)
- No configuration drift
- Clean separation of CI, CD, and deployment

![ArgoCD App](gc-industries-devops-superheros/docs/pictures/argocd_app_1.png)
*Argo CD managing application state in Kubernetes*

---

## 🌐 Traffic Flow (Runtime)
```
Users → AWS LoadBalancer (Istio IngressGateway)
      → Istio VirtualService
      → Frontend
      → Backend Services (Catalog v1/v2/v3, Inventory, Orders, Payment)
```

Istio manages:
- Canary traffic shifting
- Safe rollouts
- Observability via Kiali

![Kiali Mesh Animation](gc-industries-devops-superheros/docs/pictures/kiali_6.png)
*Kiali: Mesh traffic animation*

![Kiali Service Health](gc-industries-devops-superheros/docs/pictures/kiali_7.png)
*Kiali: Service health overview*

![Kiali Detailed Graph](gc-industries-devops-superheros/docs/pictures/kiali_8.png)
*Kiali: Detailed mesh graph*

![Kiali Traffic Distribution](gc-industries-devops-superheros/docs/pictures/kiali_9.png)
*Kiali: Traffic distribution and canary*

---

## 📣 Explore the Project
This repository demonstrates:
- Real-world CI/CD patterns
- GitOps with Argo CD
- Istio service mesh & canary deployments
- Kubernetes and AWS EKS best practices

---

🎖️⭐ Feedback, discussions, and suggestions are welcome 🎖️⭐
