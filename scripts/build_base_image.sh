#!/bin/bash
# build_base_image.sh
# Builds a minimal Alpine Linux ext4 image for Firecracker.
# This is the read-only base image that contains Python + math/science libs.
# Run once on the host. Requires: docker, mkfs.ext4, mount (must be root or use sudo).

set -euo pipefail

IMAGE_SIZE="500M"
OUTPUT_PATH="${1:-/var/lib/trustforge/images/base.ext4}"
MOUNT_DIR="$(mktemp -d)"
CONTAINER_NAME="trustforge-base-builder"

echo "==> Building TrustForge base image -> $OUTPUT_PATH"

# Step 1: Build Alpine rootfs via Docker
echo "==> Extracting Alpine rootfs via Docker..."
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
docker create --name "$CONTAINER_NAME" alpine:3.19
docker start "$CONTAINER_NAME"

# Install Python + scientific libraries inside the container
docker exec "$CONTAINER_NAME" sh -c "
  apk update && apk add --no-cache \
    python3 \
    py3-pip \
    py3-numpy \
    py3-scipy \
    py3-sympy \
    gcc \
    musl-dev \
    python3-dev && \
  pip3 install --no-cache-dir \
    torch --index-url https://download.pytorch.org/whl/cpu \
    transformers \
    datasets \
    scikit-learn \
    pandas
"

ROOTFS_TAR="$(mktemp -t trustforge-rootfs-XXXXXX.tar)"
docker export "$CONTAINER_NAME" > "$ROOTFS_TAR"
docker rm -f "$CONTAINER_NAME"

echo "==> Creating sparse ext4 image ($IMAGE_SIZE)..."
truncate -s "$IMAGE_SIZE" "$OUTPUT_PATH"
mkfs.ext4 -F -L trustforge-base -m 0 "$OUTPUT_PATH"

echo "==> Populating image..."
mount -o loop "$OUTPUT_PATH" "$MOUNT_DIR"
tar -xf "$ROOTFS_TAR" -C "$MOUNT_DIR"
rm -f "$ROOTFS_TAR"

# Install the guest agent into the image
if [ -f "./bin/guest_agent" ]; then
  cp ./bin/guest_agent "$MOUNT_DIR/usr/local/bin/guest_agent"
  chmod +x "$MOUNT_DIR/usr/local/bin/guest_agent"
  echo "==> Guest agent installed in image"
fi

# Set up the guest agent to run on boot via inittab
cat > "$MOUNT_DIR/etc/inittab" <<'EOF'
::sysinit:/bin/mount -t proc proc /proc
::sysinit:/bin/mount -t sysfs sysfs /sys
::sysinit:/bin/mount -t devtmpfs devtmpfs /dev
::once:/usr/local/bin/guest_agent
::ctrlaltdel:/sbin/reboot
EOF

umount "$MOUNT_DIR"
rmdir "$MOUNT_DIR"

# Make the image read-only at the filesystem level
tune2fs -O read-only "$OUTPUT_PATH" 2>/dev/null || true

echo "==> Base image built: $OUTPUT_PATH"
echo "    Size: $(du -sh $OUTPUT_PATH | cut -f1)"
