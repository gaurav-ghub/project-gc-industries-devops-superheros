#!/bin/bash

SERVICE_NAME=$1
IMAGE=$2

if [ -z "$SERVICE_NAME" ] || [ -z "$IMAGE" ]; then
  echo "Usage: ./create-service.sh <service-name> <image>"
  exit 1
fi

TEMPLATE_DIR="golden-path-template/helm"
IDP_DIR="idp"
SERVICE_DIR="$IDP_DIR/$SERVICE_NAME"

# Prevent overwrite
if [ -d "$SERVICE_DIR" ]; then
  echo "❌ Service already exists!"
  exit 1
fi

# Create folder
mkdir -p $SERVICE_DIR

# Copy INTO helm folder 
cp -r $TEMPLATE_DIR $SERVICE_DIR/helm

# Update values.yaml
sed -i "s/appName: my-service/appName: $SERVICE_NAME/g" $SERVICE_DIR/helm/values.yaml
sed -i "s|image: nginx:latest|image: $IMAGE|g" $SERVICE_DIR/helm/values.yaml

echo "✅ Service $SERVICE_NAME created at $SERVICE_DIR"