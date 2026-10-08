# chauffeur

`chauffeur` uploads builds, debug symbols, and store images to [Blazium Games](https://blazium.games). It is the games uploader, not [blazium-cli](https://github.com/blazium-games/blazium-cli), which installs editors.

**License:** [MIT](LICENSE)

Repository: [github.com/blazium-games/games_cli](https://github.com/blazium-games/games_cli)

## Install

### 1. npm

Linux (x64 and arm64), Windows (x64), and macOS (Intel and Apple silicon):

```text
npx @blazium-games/cli
npm install -g @blazium-games/cli
```

The command is `chauffeur`. Install `@blazium-games/cli`. License: MIT.

npm installs one of these optional binaries for the current platform:

| Package | Platform | CPU |
|---------|----------|-----|
| `@blazium-games/cli-linux-x64` | linux | x64 |
| `@blazium-games/cli-linux-arm64` | linux | arm64 |
| `@blazium-games/cli-win32-x64` | win32 | x64 |
| `@blazium-games/cli-darwin-x64` | darwin | x64 |
| `@blazium-games/cli-darwin-arm64` | darwin | arm64 |

When that optional package is installed, its binary is used and nothing is downloaded. The CDN download runs only if that package is absent.

### 2. GitHub Releases

Source and release notes:

https://github.com/blazium-games/games_cli/releases

Published binaries are the npm packages above and the CDN archives below. A checkout you build yourself uses the same file names:

| Platform | Arch | File |
|----------|------|------|
| Windows | x86_64 | `chauffeur.exe` |
| Linux | x86_64 | `chauffeur` |
| Linux | arm64 | `chauffeur` |
| macOS | arm64 | `chauffeur` |
| macOS | x86_64 | `chauffeur` |

### 3. CDN archives

The wrapper downloads these public zip archives when the optional package is missing. Each archive contains the binary and a `VERSION` file.

| Platform | Archive |
|----------|---------|
| Windows x86_64 | `https://cdn.blazium.online/tools/chauffeur/windows-amd64/latest/archive/default` |
| Linux x86_64 | `https://cdn.blazium.online/tools/chauffeur/linux-amd64/latest/archive/default` |
| Linux arm64 | `https://cdn.blazium.online/tools/chauffeur/linux-arm64/latest/archive/default` |
| macOS Apple silicon | `https://cdn.blazium.online/tools/chauffeur/darwin-arm64/latest/archive/default` |
| macOS Intel | `https://cdn.blazium.online/tools/chauffeur/darwin-amd64/latest/archive/default` |

### 4. Build from source

Requires [Go](https://go.dev/).

```text
git clone https://github.com/blazium-games/games_cli.git
cd games_cli
go test ./...
go build -o chauffeur .
```

## Deploy key

Export the game's deploy key, then:

```bash
export BLAZIUM_ACCESS_TOKEN=...
export BLAZIUM_SECRET_KEY=...
chauffeur info
```

`chauffeur` is the terminal path for uploads. The agent path is the MCP servers. MCP can list and delete builds and images. It does not upload them.

Skills that use this tool:

- `blazium-games-deploy` ships builds and symbols
- `blazium-games-store-page` edits the listing
- `blazium-games-keys` rotates the deploy key

## Platform

- Docs: [docs.blazium.games](https://docs.blazium.games) and the map [llms.txt](https://docs.blazium.games/llms.txt)
- Skills: `npm install @blazium-games/skills`, or Cursor Settings > Plugins > `blazium-games/games_skill`. Index: [SKILL_TREE.md](https://github.com/blazium-games/games_skill/blob/master/SKILL_TREE.md)
- MCP: [developer server](https://docs.blazium.games/docs/mcp) at `https://mcp.blazium.games/mcp`, and [player server](https://docs.blazium.games/docs/mcp/player) at `https://mcp.blazium.games/player`
- CLI: `npm install -g @blazium-games/cli` (`chauffeur`), guide at [docs.blazium.games/docs/cli](https://docs.blazium.games/docs/cli)
- Launcher: BlaziumLauncher at `{autopf}\Blazium\Games` on Windows. Shared tools are in `{autopf}\Blazium`. Setup from [Releases](https://github.com/blazium-games/games_launcher/releases), guide at [desktop app](https://docs.blazium.games/docs/storefront/desktop-app)
- Support: [blazium-games/support](https://github.com/blazium-games/support/issues). Status: [status.blazium.games](https://status.blazium.games)

## Environment inventory

env.example lists every variable this service reads. Local runs load .env in this directory. The workspace-root .env is a sectioned copy of these files; the process does not load it.

| Variable |
|----------|
| BLAZIUM_GAMES_CLI_VERSION |
| CDN_BASEURL |
| CDN_KEY |
| CDN_REGION |
| CDN_SECRET |
| CDN_SPACE |
| ENV_FILE |
| SPACE_PATH |
| NPM_ACCESS_TOKEN |
| NPM_TOKEN |
<!-- env-inventory-end -->
