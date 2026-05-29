#!/bin/bash
set -e

# --- Configuration ---
# Your GitHub repository (username/repo)
REPO="hypoballad/virgil"
BINARY_NAME="virgil-linux-amd64"
INSTALL_DIR="/usr/local/bin"
DEST_PATH="$INSTALL_DIR/virgil"

echo "=== Virgil Setup & Update ==="

# 1. Check for 'gh' CLI
if ! command -v gh &> /dev/null; then
    echo "❌ Error: 'gh' (GitHub CLI) is not installed."
    echo "Please install it: https://github.com/cli/cli#installation"
    exit 1
fi

# 2. Check Authentication
if ! gh auth status &> /dev/null; then
    echo "🔑 Not authenticated with GitHub."
    echo "Running 'gh auth login'..."
    gh auth login
fi

# 3. Download latest release
echo "📥 Downloading latest binary from $REPO..."
# --clobber overwrites existing file
gh release download --repo "$REPO" --pattern "$BINARY_NAME" --clobber

if [ ! -f "$BINARY_NAME" ]; then
    echo "❌ Failed to download binary. Please check if a release with tag v* exists."
    exit 1
fi

# 4. Install
echo "🚀 Installing to $DEST_PATH..."
chmod +x "$BINARY_NAME"
sudo mv "$BINARY_NAME" "$DEST_PATH"

# 5. Setup .env
if [ ! -f .env ]; then
    echo "📄 Creating initial .env file..."
    if [ -f .env.example ]; then
        cp .env.example .env
        echo ".env created from .env.example."
    else
        cat > .env << 'EOF'
OLLAMA_MODEL=qwen3.6:27b-q8_0
OLLAMA_HOST=http://127.0.0.1:11434
VIRGIL_AGENT_TIMEOUT_MINUTES=20
VIRGIL_RUN_TIMEOUT_MINUTES=60
EOF
        echo "Default .env created."
    fi
    echo "⚠️ Please check .env and adjust settings as needed."
else
    echo "✅ Existing .env found, skipping creation."
fi

echo "---"
echo "🎉 Successfully installed/updated Virgil!"
echo "You can now run it using the 'virgil' command."
