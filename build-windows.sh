#!/bin/bash
# Bash script to build Windows executables with resource files
# Usage: ./build-windows.sh [version]

VERSION="${1:-1.0.0.0}"

# Parse version into major.minor.patch.build format
IFS='.' read -ra VERSION_PARTS <<< "$VERSION"
MAJOR="${VERSION_PARTS[0]:-1}"
MINOR="${VERSION_PARTS[1]:-0}"
PATCH_BUILD="${VERSION_PARTS[2]:-0}"
PATCH=$(echo "$PATCH_BUILD" | cut -d'-' -f1)
BUILD=$(echo "$PATCH_BUILD" | grep -oP '(?<=-)\d+' || echo "0")
if [ -z "$BUILD" ]; then
    BUILD="0"
fi

FULL_VERSION="$MAJOR.$MINOR.$PATCH.$BUILD"

echo "Building Windows executables with version $FULL_VERSION"

# Check if go-winres is installed
if ! command -v go-winres &> /dev/null; then
    echo "Installing go-winres..."
    go install github.com/tc-hib/go-winres@latest
fi

# Create dist directory
mkdir -p dist/windows-amd64

# Generate resource file for chauffeur CLI
echo "Generating Windows resources for chauffeur..."
go-winres make --in winres/chauffeur.json --out . --product-version "$FULL_VERSION" --file-version "$FULL_VERSION"
if [ $? -ne 0 ]; then
    echo "Failed to generate resources for chauffeur"
    exit 1
fi

# Build chauffeur CLI
echo "Building chauffeur CLI..."
export GOOS=windows
export GOARCH=amd64
export CGO_ENABLED=0
go build -ldflags "-X main.version=$VERSION" -o "dist/windows-amd64/chauffeur.exe" .
if [ $? -ne 0 ]; then
    echo "Failed to build chauffeur CLI"
    exit 1
fi

# Generate resource file for upload tool
echo "Generating Windows resources for upload tool..."
cd cmd/upload
go-winres make --in ../../winres/upload.json --out . --product-version "$FULL_VERSION" --file-version "$FULL_VERSION"
if [ $? -ne 0 ]; then
    echo "Failed to generate resources for upload tool"
    cd ../..
    exit 1
fi
cd ../..

# Build upload tool
echo "Building upload tool..."
go build -o "dist/windows-amd64/upload.exe" ./cmd/upload
if [ $? -ne 0 ]; then
    echo "Failed to build upload tool"
    exit 1
fi

echo "Build completed successfully!"
echo "Executables are in dist/windows-amd64/"

