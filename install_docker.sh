#!/bin/bash -e
## Universal Docker installer for Debian and Ubuntu systems

echo "Detecting OS distribution..."

# Detect OS using lsb_release
if command -v lsb_release &> /dev/null; then
    DISTRO=$(lsb_release -is | tr '[:upper:]' '[:lower:]')
    CODENAME=$(lsb_release -cs)
else
    # Fallback to /etc/os-release
    . /etc/os-release
    DISTRO=$(echo "$ID" | tr '[:upper:]' '[:lower:]')
    CODENAME="$VERSION_CODENAME"
fi

echo "Detected: $DISTRO ($CODENAME)"

# Validate supported distributions
case "$DISTRO" in
    debian|ubuntu)
        echo "✓ Supported distribution detected"
        ;;
    *)
        echo "Error: Unsupported distribution '$DISTRO'"
        echo "This script supports Debian and Ubuntu only."
        exit 1
        ;;
esac

# Remove any previous Docker installations
echo "Removing old Docker packages (if any)..."
sudo apt remove -y docker.io docker-compose docker-doc podman-docker containerd runc 2>/dev/null || true

# Add Docker's official GPG key
echo "Adding Docker GPG key..."
sudo apt update
sudo apt install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL "https://download.docker.com/linux/${DISTRO}/gpg" -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

# Add the repository to Apt sources
echo "Adding Docker repository..."
sudo tee /etc/apt/sources.list.d/docker.sources > /dev/null <<EOF
Types: deb
URIs: https://download.docker.com/linux/${DISTRO}
Suites: ${CODENAME}
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF

# Update package index
echo "Updating package index..."
sudo apt update

# Install Docker
echo "Installing Docker..."
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# Start Docker service
echo "Starting Docker service..."
sudo systemctl start docker || true
sudo systemctl enable docker

# Verify installation
echo ""
echo "Docker installation complete!"
docker --version
docker compose version

echo ""
echo "✓ Docker is ready to use"
