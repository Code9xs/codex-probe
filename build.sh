#!/usr/bin/env bash
#
# codex-probe 跨平台打包脚本
#
# 用法:
#   ./build.sh              # 编译所有平台
#   ./build.sh current      # 仅编译当前平台
#   ./build.sh linux-amd64  # 编译指定平台
#
# 支持平台:
#   linux-amd64, linux-arm64
#   darwin-amd64, darwin-arm64
#   windows-amd64
#

set -euo pipefail

APP_NAME="codex-probe"
SRC_PATH="./cmd/codex-probe/"
DIST_DIR="./dist"

# ── 版本信息 ──
VERSION="${VERSION:-dev}"
BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

LDFLAGS="-s -w"

# ── 颜色 ──
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${GREEN}[BUILD]${NC} $*"; }
info() { echo -e "${CYAN}[INFO]${NC}  $*"; }
err()  { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# ── 目标平台列表 ──
ALL_TARGETS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

build_one() {
    local os=$1
    local arch=$2

    local ext=""
    [[ "$os" == "windows" ]] && ext=".exe"

    local output="${DIST_DIR}/${APP_NAME}-${os}-${arch}${ext}"

    log "Building ${BOLD}${os}/${arch}${NC} → ${output}"

    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
        go build -trimpath \
        -ldflags "$LDFLAGS" \
        -o "$output" \
        "$SRC_PATH"

    # 非 Windows 平台添加可执行权限
    [[ "$os" != "windows" ]] && chmod +x "$output"

    local size
    size=$(du -h "$output" | cut -f1 | xargs)
    info "  ✓ ${output}  (${size})"
}

build_current() {
    local current_os
    local current_arch
    current_os=$(go env GOOS)
    current_arch=$(go env GOARCH)

    local ext=""
    [[ "$current_os" == "windows" ]] && ext=".exe"

    local output="${APP_NAME}${ext}"

    log "Building for current platform: ${BOLD}${current_os}/${current_arch}${NC}"

    CGO_ENABLED=0 \
        go build -trimpath \
        -ldflags "$LDFLAGS" \
        -o "$output" \
        "$SRC_PATH"

    chmod +x "$output" 2>/dev/null || true

    local size
    size=$(du -h "$output" | cut -f1 | xargs)
    info "✓ ${output}  (${size})"
}

build_target() {
    local target=$1
    local os arch

    # 支持 linux-amd64 和 linux/amd64 两种写法
    if [[ "$target" == *"/"* ]]; then
        os="${target%%/*}"
        arch="${target##*/}"
    else
        os="${target%%-*}"
        arch="${target##*-}"
    fi

    build_one "$os" "$arch"
}

build_all() {
    log "Building all platforms..."
    echo ""

    mkdir -p "$DIST_DIR"

    for target in "${ALL_TARGETS[@]}"; do
        local os="${target%%/*}"
        local arch="${target##*/}"
        build_one "$os" "$arch"
    done
}

generate_checksums() {
    log "Generating checksums..."
    cd "$DIST_DIR"

    if command -v sha256sum &>/dev/null; then
        sha256sum ${APP_NAME}-* > checksums.txt
    elif command -v shasum &>/dev/null; then
        shasum -a 256 ${APP_NAME}-* > checksums.txt
    else
        err "sha256sum / shasum not found, skipping checksums"
        cd - > /dev/null
        return
    fi

    info "✓ ${DIST_DIR}/checksums.txt"
    cd - > /dev/null
}

print_summary() {
    echo ""
    echo -e "${BOLD}═══════════════════ BUILD SUMMARY ═══════════════════${NC}"
    echo -e "  version  : ${GREEN}${VERSION}${NC}"
    echo -e "  commit   : ${CYAN}${GIT_COMMIT}${NC}"
    echo -e "  time     : ${BUILD_TIME}"
    echo ""
    echo "  Artifacts:"
    for f in "${DIST_DIR}"/${APP_NAME}-*; do
        [[ -f "$f" ]] || continue
        local size
        size=$(du -h "$f" | cut -f1 | xargs)
        echo -e "    ${CYAN}$(basename "$f")${NC}  (${size})"
    done
    if [[ -f "${DIST_DIR}/checksums.txt" ]]; then
        echo -e "    ${CYAN}checksums.txt${NC}"
    fi
    echo -e "${BOLD}════════════════════════════════════════════════════=${NC}"
    echo ""
}

# ── 主入口 ──
main() {
    echo ""
    echo -e "${BOLD}  codex-probe build script${NC}"
    echo ""

    # 检查 Go 环境
    if ! command -v go &>/dev/null; then
        err "Go is not installed. Please install Go 1.22+ first."
        exit 1
    fi

    local go_version
    go_version=$(go version | awk '{print $3}')
    info "Go version: ${go_version}"

    case "${1:-all}" in
        current)
            build_current
            ;;
        all)
            build_all
            generate_checksums
            print_summary
            ;;
        clean)
            log "Cleaning dist directory..."
            rm -rf "$DIST_DIR"
            rm -f "${APP_NAME}" "${APP_NAME}.exe"
            info "✓ cleaned"
            ;;
        help|--help|-h)
            echo "Usage: $0 [target]"
            echo ""
            echo "Targets:"
            echo "  all              Build all platforms (default)"
            echo "  current          Build for current platform only"
            echo "  clean            Remove build artifacts"
            echo "  linux-amd64      Build for Linux x86-64"
            echo "  linux-arm64      Build for Linux ARM64"
            echo "  darwin-amd64     Build for macOS Intel"
            echo "  darwin-arm64     Build for macOS Apple Silicon"
            echo "  windows-amd64    Build for Windows x86-64"
            echo ""
            echo "Environment variables:"
            echo "  VERSION          Version tag (default: dev)"
            echo ""
            echo "Examples:"
            echo "  ./build.sh"
            echo "  ./build.sh current"
            echo "  VERSION=v1.2.0 ./build.sh"
            ;;
        *)
            mkdir -p "$DIST_DIR"
            build_target "$1"
            ;;
    esac
}

main "$@"
