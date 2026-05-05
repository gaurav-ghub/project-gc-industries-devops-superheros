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
# This is a security best practice and also allows us to run nginx on a non-privileged port (like 8080) without needing root permissions.
# If we dont use this, my Kyverno policies will complain about running as root and using privileged ports.
FROM nginxinc/nginx-unprivileged:1.25-alpine

# Switch to root ONLY for copy (important trick)
USER root

# Copy build output (this overwrites default content)
COPY --from=build /app/dist /usr/share/nginx/html

# Copy nginx config
COPY nginx.conf /etc/nginx/conf.d/default.conf

# FIX PERMISSIONS FOR NGINX RUNTIME DIRS
RUN chown -R 101:101 /usr/share/nginx/html \
    && chown -R 101:101 /var/cache/nginx \
    && chown -R 101:101 /var/run \
    && chown -R 101:101 /etc/nginx

# Switch back to non-root
USER 101

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]




