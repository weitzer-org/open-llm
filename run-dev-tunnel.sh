#!/usr/bin/env bash

# Secure Open-LLM Developer Tunnel Router & Local Console
# Automatically configures and launches the secure development environments.

set -euo pipefail

# Define operational parameters
PROJECT_ID="quacktastic-waffle"
REGION="us-central1"
SERVICE_NAME="open-llm-vllm"
TUNNEL_PORT=8001
GATEWAY_PORT=8080

echo "🚀 Initializing Secure Open-LLM Developer Tunnel Console..."
echo "🪐 Fetching active API authentication secret from GCP Secret Manager..."

# Fetch the live secret key to display to the developer for instant copy-paste
API_SECRET=$(gcloud secrets versions access latest --secret="open-llm-api-auth-secret" --project="$PROJECT_ID" 2>/dev/null || echo "")

if [ -z "$API_SECRET" ]; then
  API_SECRET="open-llm-dev-token"
  echo "💡 Info: Secret Manager key not found yet (targeted apply mode). Utilizing safe dev key: '$API_SECRET'"
fi

# Define temporary logging files
TMP_DIR="/tmp/open-llm-dev"
mkdir -p "$TMP_DIR"
TUNNEL_LOG="$TMP_DIR/tunnel.log"
GATEWAY_LOG="$TMP_DIR/gateway.log"

# Setup clean termination handler
cleanup() {
  echo -e "\n🪐 Gracefully shutting down secure proxy and local gateway servers..."
  if [ -n "${GATEWAY_PID:-}" ]; then
    kill "$GATEWAY_PID" 2>/dev/null || true
  fi
  if [ -n "${TUNNEL_PID:-}" ]; then
    kill "$TUNNEL_PID" 2>/dev/null || true
  fi
  rm -rf "$TMP_DIR"
  echo "✔ Done. Secure dev context closed cleanly."
  exit 0
}

# Trap terminal exits or user interrupts
trap cleanup SIGINT SIGTERM EXIT

# 0. Validate and ensure the secure Cloud Run Proxy package dependencies are present
if ! dpkg -l google-cloud-cli-cloud-run-proxy &>/dev/null; then
  echo "📦 The secure GCloud Cloud Run Proxy package is missing on this system!"
  echo "Installing standard package 'google-cloud-cli-cloud-run-proxy' via apt-get..."
  sudo apt-get update && sudo apt-get install -y google-cloud-cli-cloud-run-proxy
  echo "✔ Component successfully installed!"
fi

# 1. Start the secure IAM OIDC proxy tunnel
echo "🪐 Spawning secure Google Cloud IAM OIDC tunnel on port :$TUNNEL_PORT..."
gcloud beta run services proxy "$SERVICE_NAME" \
  --project="$PROJECT_ID" \
  --region="$REGION" \
  --port="$TUNNEL_PORT" \
  > "$TUNNEL_LOG" 2>&1 &
TUNNEL_PID=$!

# Wait briefly for tunnel port initialization
sleep 2.5

# Verify tunnel is running
if ! kill -0 "$TUNNEL_PID" 2>/dev/null; then
  echo "❌ Error: Secure GCloud tunnel failed to start!"
  cat "$TUNNEL_LOG"
  exit 1
fi

# 2. Launch the local Go Gateway
echo "🪐 Compiling and launching local Go Gateway on port :$GATEWAY_PORT..."
# Pass safe writeable locations for Go cache to prevent restricted process blocks
export PORT="$GATEWAY_PORT"
export API_AUTH_SECRET="$API_SECRET"
export VLLM_API_URL="http://localhost:$TUNNEL_PORT"
export HOME="/usr/local/google/home/benweitzer/Documents/open-llm/scratch"
export GOCACHE="/usr/local/google/home/benweitzer/Documents/open-llm/scratch/go-cache"

go run gateway/main.go > "$GATEWAY_LOG" 2>&1 &
GATEWAY_PID=$!

# Wait briefly for the Go web application compilation and port bind
sleep 3.0

# Verify gateway is running
if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
  echo "❌ Error: Go Gateway server failed to start!"
  cat "$GATEWAY_LOG"
  exit 1
fi

# Fetch Fully Qualified Domain Name (FQDN) for corporate direct network routing links
FQDN=$(hostname -f 2>/dev/null || echo "localhost")

# 3. Print the ready console UI
echo -e "\n========================================================="
echo -e "🪐 SECURE DEVELOPER WORKSPACE ONLINE & SERVING REQUESTS!"
echo -e "========================================================="
echo -e "🔐 Master Token (Auto-Fetched) : \033[1;32m$API_SECRET\033[0m"
echo -e "🌐 Corporate Web Console URL   : \033[1;36mhttp://$FQDN:$GATEWAY_PORT/\033[0m"
echo -e "🌐 Local Loopback Fallback URL  : \033[1;30mhttp://localhost:$GATEWAY_PORT/\033[0m"
echo -e "🔒 Private GPU service target  : $SERVICE_NAME (internal-only)"
echo -e "---------------------------------------------------------"
echo -e "💡 Direct Corporate Access Active! No manual SSH port-forwarding tunnels required."
echo -e "📝 System logs are being piped to: $TMP_DIR/"
echo -e "👉 Press \033[1;33m[ENTER]\033[0m or Ctrl+C at any time to safely shut down the tunnel and exit."
echo -e "========================================================="

# Hold execution until the user requests exit
read -r
