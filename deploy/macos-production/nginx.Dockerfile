FROM nginx:1.30.4-alpine

# Docker Desktop can return EIO for individual macOS bind-mounted
# configuration files. Bake the public configuration into the image; mount
# only runtime TLS material and the package volume.
COPY deploy/macos-production/nginx.conf /etc/nginx/nginx.conf
