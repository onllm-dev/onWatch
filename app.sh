#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VERSION=$(cat "$SCRIPT_DIR/VERSION")
BINARY="onwatch"
LDFLAGS="-ldflags=-s -w -X main.version=$VERSION"

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()    { echo -e "${CYAN}${BOLD}==> $1${NC}"; }
success() { echo -e "${GREEN}${BOLD}==> $1${NC}"; }
error()   { echo -e "${RED}${BOLD}==> ERROR: $1${NC}" >&2; }
warn()    { echo -e "${YELLOW}${BOLD}==> $1${NC}"; }

# --- Usage ---
usage() {
    cat <<EOF
${BOLD}onWatch v${VERSION} -- Project Management Script${NC}

${CYAN}USAGE:${NC}
    ./app.sh [FLAGS...]

${CYAN}FLAGS:${NC}
    --build,   -b                  Build production binary with version ldflags
    --test,    -t                  Run all tests with race detection and coverage
    --smoke,   -s                  Quick validation: vet + build check + short tests
    --run,     -r                  Build and run in debug mode (foreground)
    --release                      Run tests, then cross-compile for 5 platforms
    --clean,   -c                  Remove binary, coverage files, dist/, test cache
    --deps,    -d, --install,
               --dependencies,
               --requirements      Install dependencies (Go, git) for your OS
    --help,    -h                  Show this help message

${CYAN}EXAMPLES:${NC}
    ./app.sh --build               # Build production binary
    ./app.sh --test                 # Run full test suite
    ./app.sh --smoke               # Quick pre-commit check
    ./app.sh --clean --build --run  # Clean, rebuild, and run
    ./app.sh --deps --build --test  # Install deps, build, test
    ./app.sh --release              # Full release build (5 platforms)

${CYAN}NOTES:${NC}
    Flags can be combined. Execution order is always:
    deps -> clean -> build -> test -> smoke -> release -> run
EOF
}

# --- Flag parsing ---
DO_DEPS=false
DO_CLEAN=false
DO_BUILD=false
DO_TEST=false
DO_SMOKE=false
DO_RELEASE=false
DO_RUN=false

if [[ $# -eq 0 ]]; then
    usage
    exit 0
fi

for arg in "$@"; do
    case "$arg" in
        --build|-b)
            DO_BUILD=true ;;
        --test|-t)
            DO_TEST=true ;;
        --smoke|-s)
            DO_SMOKE=true ;;
        --run|-r)
            DO_RUN=true ;;
        --release)
            DO_RELEASE=true ;;
        --clean|-c)
            DO_CLEAN=true ;;
        --deps|-d|--install|--dependencies|--requirements)
            DO_DEPS=true ;;
        --help|-h)
            usage
            exit 0 ;;
        *)
            error "Unknown flag: $arg"
            echo ""
            usage
            exit 1 ;;
    esac
done

# --- Step functions ---

do_deps() {
    info "Installing dependencies..."
    if [[ "$(uname)" == "Darwin" ]]; then
        info "Detected macOS -- using Homebrew"
        if ! command -v brew &>/dev/null; then
            error "Homebrew not found. Install from https://brew.sh"
            exit 1
        fi
        if ! command -v go &>/dev/null; then
            info "Installing Go..."
            brew install go
        else
            success "Go already installed: $(go version)"
        fi
        if ! command -v git &>/dev/null; then
            info "Installing git..."
            brew install git
        else
            success "git already installed: $(git --version)"
        fi
    elif [[ -f /etc/debian_version ]]; then
        info "Detected Debian/Ubuntu -- using apt"
        sudo apt-get update -qq
        if ! command -v go &>/dev/null; then
            info "Installing Go..."
            sudo apt-get install -y golang
        else
            success "Go already installed: $(go version)"
        fi
        if ! command -v git &>/dev/null; then
            info "Installing git..."
            sudo apt-get install -y git
        else
            success "git already installed: $(git --version)"
        fi
    elif [[ -f /etc/redhat-release ]] || [[ -f /etc/fedora-release ]]; then
        info "Detected Fedora/RHEL -- using dnf"
        if ! command -v go &>/dev/null; then
            info "Installing Go..."
            sudo dnf install -y golang
        else
            success "Go already installed: $(go version)"
        fi
        if ! command -v git &>/dev/null; then
            info "Installing git..."
            sudo dnf install -y git
        else
            success "git already installed: $(git --version)"
        fi
    else
        error "Unsupported OS. Please install Go and git manually."
        exit 1
    fi
    success "Dependencies ready."
}

do_clean() {
    info "Cleaning build artifacts..."
    rm -f "$SCRIPT_DIR/$BINARY"
    rm -f "$SCRIPT_DIR/coverage.out" "$SCRIPT_DIR/coverage.html"
    rm -rf "$SCRIPT_DIR/dist/"
    go clean -testcache
    success "Clean complete."
}

do_build() {
    info "Building onWatch v${VERSION}..."
    cd "$SCRIPT_DIR"
    go build -ldflags="-s -w -X main.version=$VERSION" -o "$BINARY" .
    success "Built ./$BINARY ($(du -h "$BINARY" | cut -f1 | xargs))"
}

do_test() {
    info "Running tests with race detection and coverage..."
    cd "$SCRIPT_DIR"
    go test -race -cover -count=1 ./...
    success "All tests passed."
}

do_smoke() {
    info "Running smoke checks..."
    cd "$SCRIPT_DIR"

    info "  go vet ./..."
    go vet ./...

    info "  Build check..."
    go build -ldflags="-s -w -X main.version=$VERSION" -o /dev/null .

    info "  Short tests..."
    go test -short -count=1 ./...

    success "Smoke checks passed."
}

do_release() {
    info "Running tests before release..."
    cd "$SCRIPT_DIR"
    go test -race -cover -count=1 ./...
    success "Tests passed."

    info "Cross-compiling onWatch v${VERSION} for 5 platforms..."
    mkdir -p "$SCRIPT_DIR/dist"

    local targets=(
        "darwin:arm64:"
        "darwin:amd64:"
        "linux:amd64:"
        "linux:arm64:"
        "windows:amd64:.exe"
    )

    for target in "${targets[@]}"; do
        IFS=':' read -r os arch ext <<< "$target"
        local output="dist/onwatch-${os}-${arch}${ext}"
        info "  Building ${output}..."
        CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
            -ldflags="-s -w -X main.version=$VERSION" \
            -o "$SCRIPT_DIR/$output" .
    done

    success "Release build complete. Binaries in dist/:"
    ls -lh "$SCRIPT_DIR/dist/"
}

do_run() {
    info "Building and running onWatch v${VERSION} in debug mode..."
    do_build
    info "Starting ./onwatch --debug"
    exec "$SCRIPT_DIR/$BINARY" --debug
}

# --- Execute in order: deps -> clean -> build -> test -> smoke -> release -> run ---

$DO_DEPS    && do_deps
$DO_CLEAN   && do_clean
$DO_BUILD   && do_build
$DO_TEST    && do_test
$DO_SMOKE   && do_smoke
$DO_RELEASE && do_release
$DO_RUN     && do_run

exit 0
