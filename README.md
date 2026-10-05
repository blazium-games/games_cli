# chauffeur

`chauffeur` uploads builds, debug symbols, and store images to [Blazium Games](https://blazium.games). It is the games CLI. It is not [blazium-cli](https://github.com/blazium-games/blazium-cli), which installs editors.

## Install

```bash
npm install -g @blazium-games/cli
chauffeur --version
```

The npm package is `@blazium-games/cli`. The command is `chauffeur`. Platform packages cover Linux x64 and arm64, Windows x64, and macOS Intel and Apple silicon. If the optional package is missing, the wrapper downloads the public CDN archive.

CDN archives stay at `https://cdn.blazium.online/tools/chauffeur/<platform>/latest/archive/default`:

| Platform | Path segment |
|----------|----------------|
| Windows x86_64 | `windows-amd64` |
| Linux x86_64 | `linux-amd64` |
| Linux arm64 | `linux-arm64` |
| macOS Apple silicon | `darwin-arm64` |
| macOS Intel | `darwin-amd64` |

## Deploy key

Export the game's deploy key, then:

```bash
export BLAZIUM_ACCESS_TOKEN=...
export BLAZIUM_SECRET_KEY=...
chauffeur info
```

Docs: [docs.blazium.games](https://docs.blazium.games).

## License

Licensed under the MIT License — see [LICENSE](LICENSE).
