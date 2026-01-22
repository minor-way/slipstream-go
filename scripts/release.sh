#!/bin/bash
#
# Build and release script for Slipstream-Go
# Creates binaries for multiple platforms and uploads to GitHub
#

set -e

VERSION="${1:-v1.0.0}"
BINARY_NAME="slipstream"
BUILD_DIR="dist"
PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

echo "Building Slipstream-Go ${VERSION}"
echo "================================"

# Clean and create build directory
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# Build for each platform
for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS="${PLATFORM%/*}"
    GOARCH="${PLATFORM#*/}"
    
    # Output names
    SERVER_OUTPUT="${BUILD_DIR}/${BINARY_NAME}-server-${GOOS}-${GOARCH}"
    CLIENT_OUTPUT="${BUILD_DIR}/${BINARY_NAME}-client-${GOOS}-${GOARCH}"
    
    # Add .exe for Windows
    if [ "$GOOS" = "windows" ]; then
        SERVER_OUTPUT="${SERVER_OUTPUT}.exe"
        CLIENT_OUTPUT="${CLIENT_OUTPUT}.exe"
    fi
    
    echo "Building for ${GOOS}/${GOARCH}..."
    
    # Build server
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
        -ldflags="-w -s -X main.version=${VERSION}" \
        -o "$SERVER_OUTPUT" \
        ./cmd/server
    
    # Build client
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
        -ldflags="-w -s -X main.version=${VERSION}" \
        -o "$CLIENT_OUTPUT" \
        ./cmd/client
    
    # Create archive
    ARCHIVE_NAME="${BINARY_NAME}-${VERSION}-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
        # Zip for Windows
        (cd "$BUILD_DIR" && zip -q "${ARCHIVE_NAME}.zip" \
            "$(basename $SERVER_OUTPUT)" \
            "$(basename $CLIENT_OUTPUT)")
        rm "$SERVER_OUTPUT" "$CLIENT_OUTPUT"
        echo "  Created: ${ARCHIVE_NAME}.zip"
    else
        # Tar.gz for Unix
        (cd "$BUILD_DIR" && tar -czf "${ARCHIVE_NAME}.tar.gz" \
            "$(basename $SERVER_OUTPUT)" \
            "$(basename $CLIENT_OUTPUT)")
        rm "$SERVER_OUTPUT" "$CLIENT_OUTPUT"
        echo "  Created: ${ARCHIVE_NAME}.tar.gz"
    fi
done

echo ""
echo "Build complete! Archives in ${BUILD_DIR}/"
ls -lh "$BUILD_DIR"

echo ""
echo "To create GitHub release, run:"
echo "  gh release create ${VERSION} ${BUILD_DIR}/* --title \"${VERSION}\" --notes \"Release ${VERSION}\""
