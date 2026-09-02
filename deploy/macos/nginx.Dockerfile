FROM nginx:1.30.4-alpine

# Docker Desktop can return EIO for single-file host bind mounts on macOS.
# Copy the public, credential-free configuration into the test image instead.
COPY deploy/nginx/nginx.dev.conf /etc/nginx/nginx.conf
