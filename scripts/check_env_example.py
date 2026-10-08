"""Fail when code reads an environment variable that env.example does not list.

Prints missing names only. Never prints values.
"""
import re
import sys
from pathlib import Path

SKIP_DIRS = {"vendor", "vendored", "node_modules", ".git", "build", "dist", "coverage", ".react-router"}
SKIP_KEYS = {
    "HOME", "PATH", "USER", "USERNAME", "USERPROFILE", "LOCALAPPDATA", "APPDATA",
    "TEMP", "TMP", "TMPDIR", "TERM", "CI", "GODEBUG", "GOOS", "GOARCH", "GO_VERSION",
    "PATHEXT", "SystemRoot", "COMSPEC", "WINDIR", "PWD", "OLDPWD", "SHELL", "LANG",
    "LC_ALL", "NODE_OPTIONS", "DEV", "MODE", "PROD", "SSR", "ENTRY",
    "GITHUB_TOKEN", "GH_TOKEN",
}
EXTS = {".go", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".cpp", ".h", ".hpp", ".py", ".sh", ".yml", ".yaml"}
PATTERNS = [
    re.compile(r'os\.Getenv\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'os\.LookupEnv\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'env(?:Str|Int|Or)\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'(?:config|commonconfig)\.Getenv\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'process\.env\.([A-Z][A-Z0-9_]+)'),
    re.compile(r'process\.env\["([A-Z][A-Z0-9_]+)"\]'),
    re.compile(r'import\.meta\.env\.([A-Z][A-Z0-9_]+)'),
    re.compile(r'(?:std::)?getenv\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'os\.environ(?:\.get)?\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'os\.getenv\(\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'const\s+\w*Env\s*=\s*"([A-Z][A-Z0-9_]+)"'),
    re.compile(r'(?m)^[ \t]*-[ \t]*key:[ \t]*([A-Z][A-Z0-9_]+)\s*$'),
]


def example_keys(root: Path) -> set[str]:
    path = root / "env.example"
    if not path.exists():
        return set()
    keys = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        raw = line.strip()
        if not raw or raw.startswith("#") or "=" not in raw:
            continue
        keys.add(raw.split("=", 1)[0].strip())
    return keys


def code_keys(root: Path) -> set[str]:
    found = set()
    for p in root.rglob("*"):
        if not p.is_file() or p.suffix not in EXTS:
            continue
        if any(part in SKIP_DIRS for part in p.parts):
            continue
        if p.suffix in {".yml", ".yaml"} and ".github" not in p.parts and ".do" not in p.parts:
            continue
        if p.name == "check_env_example.py":
            continue
        try:
            text = p.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        for pat in PATTERNS:
            found.update(pat.findall(text))
    return {k for k in found if k not in SKIP_KEYS and re.fullmatch(r"[A-Z][A-Z0-9_]+", k)}


def main() -> int:
    root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(".")
    missing = sorted(code_keys(root) - example_keys(root))
    if missing:
        print("env.example is missing:")
        for name in missing:
            print(name)
        return 1
    print(f"env.example covers {len(example_keys(root))} names")
    return 0


if __name__ == "__main__":
    sys.exit(main())
