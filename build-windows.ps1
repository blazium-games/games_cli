# PowerShell script to build Windows executables with resource files
# Usage: .\build-windows.ps1 [version]

param(
    [string]$Version = "1.0.0.0"
)

# Parse version into major.minor.patch.build format
$versionParts = $Version -split '\.'
$major = if ($versionParts.Length -gt 0) { $versionParts[0] } else { "1" }
$minor = if ($versionParts.Length -gt 1) { $versionParts[1] } else { "0" }
$patch = if ($versionParts.Length -gt 2) { $versionParts[2] -split '-' | Select-Object -First 1 } else { "0" }
$build = if ($versionParts.Length -gt 2) { ($versionParts[2] -split '-')[1] -replace '[^0-9]', '' } else { "0" }
if ([string]::IsNullOrEmpty($build)) { $build = "0" }

$fullVersion = "$major.$minor.$patch.$build"

Write-Host "Building Windows executables with version $fullVersion" -ForegroundColor Green

# Check if go-winres is installed
$goWinresPath = Get-Command go-winres -ErrorAction SilentlyContinue
if (-not $goWinresPath) {
    Write-Host "Installing go-winres..." -ForegroundColor Yellow
    go install github.com/tc-hib/go-winres@latest
}

# Generate resource file for chauffeur CLI
Write-Host "Generating Windows resources for chauffeur..." -ForegroundColor Cyan
go-winres make --in winres/chauffeur.json --out . --product-version $fullVersion --file-version $fullVersion
if ($LASTEXITCODE -ne 0) {
    Write-Host "Failed to generate resources for chauffeur" -ForegroundColor Red
    exit 1
}

# Build chauffeur CLI
Write-Host "Building chauffeur CLI..." -ForegroundColor Cyan
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -ldflags "-X main.version=$Version" -o "dist/windows-amd64/chauffeur.exe" .
if ($LASTEXITCODE -ne 0) {
    Write-Host "Failed to build chauffeur CLI" -ForegroundColor Red
    exit 1
}

# Generate resource file for upload tool
Write-Host "Generating Windows resources for upload tool..." -ForegroundColor Cyan
Push-Location cmd/upload
go-winres make --in ../../winres/upload.json --out . --product-version $fullVersion --file-version $fullVersion
if ($LASTEXITCODE -ne 0) {
    Write-Host "Failed to generate resources for upload tool" -ForegroundColor Red
    Pop-Location
    exit 1
}
Pop-Location

# Build upload tool
Write-Host "Building upload tool..." -ForegroundColor Cyan
go build -o "dist/windows-amd64/upload.exe" ./cmd/upload
if ($LASTEXITCODE -ne 0) {
    Write-Host "Failed to build upload tool" -ForegroundColor Red
    exit 1
}

Write-Host "Build completed successfully!" -ForegroundColor Green
Write-Host "Executables are in dist/windows-amd64/" -ForegroundColor Green

