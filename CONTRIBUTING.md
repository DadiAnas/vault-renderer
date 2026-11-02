# Contributing

Contributions are welcome. Open an issue to discuss a new compatibility feature before expanding the accepted configuration keys.

## Local checks

```sh
go test -count=1 ./...
go vet ./...
go build ./cmd/vault-renderer
```

For behavior changes, add a failing test first. Keep the compatibility matrix in [README.md](README.md) aligned with the tests. Unknown Vault annotations must fail clearly until their behavior is implemented. Never put Vault tokens, private keys, or retrieved secret values in examples or generated ConfigMaps.

The GitHub Pages site lives in `docs/` and uses plain HTML and CSS. Preview it with `python3 -m http.server 8000 --directory docs`.
