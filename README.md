# chauffeur

`chauffeur` uploads builds, debug symbols, and store images to [Blazium Games](https://blazium.games). It is the games CLI. It is not [blazium-cli](https://github.com/blazium-games/blazium-cli), which installs editors.

## Install

```bash
npm install -g @blazium-games/cli
chauffeur --version
```

The npm package is `@blazium-games/cli`. The command is `chauffeur`. Platform packages cover Linux x64 and arm64, Windows x64, and macOS Intel and Apple silicon. If the optional package is missing, the wrapper downloads the public CDN archive.

| Platform | Archive |
|----------|---------|
| Windows x86_64 | `https://cdn.blazium.online/tools/chauffeur/windows-amd64/latest/archive/default` |
| Linux x86_64 | `https://cdn.blazium.online/tools/chauffeur/linux-amd64/latest/archive/default` |
| Linux arm64 | `https://cdn.blazium.online/tools/chauffeur/linux-arm64/latest/archive/default` |
| macOS Apple silicon | `https://cdn.blazium.online/tools/chauffeur/darwin-arm64/latest/archive/default` |
| macOS Intel | `https://cdn.blazium.online/tools/chauffeur/darwin-amd64/latest/archive/default` |

## Platform

- Docs: [docs.blazium.games](https://docs.blazium.games) and the map [llms.txt](https://docs.blazium.games/llms.txt)
- Skills: `npm install @blazium-games/skills`, or Cursor Settings > Plugins > `blazium-games/games_skill`. Index: [SKILL_TREE.md](https://github.com/blazium-games/games_skill/blob/master/SKILL_TREE.md)
- MCP: [developer server](https://docs.blazium.games/docs/mcp) at `https://mcp.blazium.games/mcp`, and [player server](https://docs.blazium.games/docs/mcp/player) at `https://mcp.blazium.games/player`
- CLI: `npm install -g @blazium-games/cli` (`chauffeur`), guide at [docs.blazium.games/docs/cli](https://docs.blazium.games/docs/cli)
- Launcher: BlaziumLauncher at `{autopf}\Blazium\Games` on Windows. Shared tools are in `{autopf}\Blazium`. Setup from [Releases](https://github.com/blazium-games/games_launcher/releases), guide at [desktop app](https://docs.blazium.games/docs/storefront/desktop-app)
- Support: [blazium-games/support](https://github.com/blazium-games/support/issues). Status: [status.blazium.games](https://status.blazium.games)

## This repo

`chauffeur` is the terminal path for uploads. The agent path is the MCP servers. MCP can list and delete builds and images. It does not upload them.

Skills that use this tool:

- `blazium-games-deploy` ships builds and symbols
- `blazium-games-store-page` edits the listing
- `blazium-games-keys` rotates the deploy key

Export the game's deploy key, then:

```bash
export BLAZIUM_ACCESS_TOKEN=...
export BLAZIUM_SECRET_KEY=...
chauffeur info
```

## License

Licensed under the MIT License — see [LICENSE](LICENSE).
