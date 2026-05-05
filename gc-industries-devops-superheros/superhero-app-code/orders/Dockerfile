# --- Build Stage ---
FROM node:18-alpine AS build
WORKDIR /app

# Install dependencies (clean install)
COPY package*.json ./
RUN npm ci

# Copy source code
COPY . .

# --- Runtime Stage ---
FROM node:18-alpine
WORKDIR /app

# Copy dependencies + source from build stage
COPY --from=build /app /app

# Run as non-root user for security
RUN addgroup -S app && adduser -S -G app app
USER app

EXPOSE 8082

CMD ["node", "server.js"]
