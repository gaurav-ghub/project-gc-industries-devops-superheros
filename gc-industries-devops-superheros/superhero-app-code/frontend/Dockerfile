# --- Build Stage ---
FROM node:18-alpine AS build
WORKDIR /app

COPY package*.json ./
RUN npm ci

COPY . .

ARG VITE_PAYMENT_API
ENV VITE_PAYMENT_API=$VITE_PAYMENT_API

RUN npm run build


# --- Runtime Stage ---

# its important to use an unprivileged nginx image to avoid running as root.
# This is a security best practice — but it does not, by itself, let nginx bind
# a privileged port like 80 as a non-root user; the base image's own default is
# port 8080, and this Dockerfile/nginx.conf now keep it there. Kubernetes'
# capabilities.drop: [ALL] removes NET_BIND_SERVICE regardless of which base
# image is used, so 80 is not reachable here no matter what.
# If we dont use this, my Kyverno policies will complain about running as root and using privileged ports.
FROM nginxinc/nginx-unprivileged:1.25-alpine

# Switch to root ONLY for copy (important trick)
USER root

# Copy build output (this overwrites default content)
COPY --from=build /app/dist /usr/share/nginx/html

# Copy nginx config
COPY nginx.conf /etc/nginx/conf.d/default.conf

# Fix permissions (VERY IMPORTANT)
RUN chown -R 101:101 /usr/share/nginx/html

# Switch back to non-root
USER 101

EXPOSE 8080

CMD ["nginx", "-g", "daemon off;"]



