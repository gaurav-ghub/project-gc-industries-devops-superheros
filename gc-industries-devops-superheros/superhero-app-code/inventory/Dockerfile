# --- Build Stage ---
FROM python:3.11-slim AS build
WORKDIR /app

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential gcc && \
    rm -rf /var/lib/apt/lists/*

# Copy requirement files
COPY requirements.txt .

# Build wheels
RUN pip wheel --no-cache-dir --wheel-dir /wheels -r requirements.txt

# Copy source code
COPY . .


# --- Runtime Stage ---
FROM python:3.11-slim

WORKDIR /app

# Install runtime deps only
COPY --from=build /wheels /wheels
RUN pip install --no-cache-dir /wheels/*

# Copy source code
COPY . .

# Create non-root user
RUN useradd -m appuser
USER appuser

EXPOSE 8083

# Run FastAPI with Uvicorn (production mode)
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8083"]
