# @checkend/cli

The npm package of `ce`, the [Checkend](https://github.com/Checkend/checkend) CLI. The first run
downloads the `ce` binary for your platform from the GitHub release of the same version, checks
its SHA-256 against `checksums.txt` in this package, and keeps it in a cache directory.

```sh
npx @checkend/cli --version
```

The source and the documentation are in
[Checkend/checkend-cli](https://github.com/Checkend/checkend-cli).
