# Releasing ce

People only: agents do not push tags or approve releases (see [AGENTS.md](AGENTS.md)). A release
is a version tag on `main`. The workflow `.github/workflows/release.yml` does the rest.

1. Check `main`: CI is green, and `CHANGELOG.md` has a section for the version, for example
   `## [0.3.2] - 2026-10-20`, above the previous one.

2. Tag the commit and push the tag (an annotated tag; use `-s` instead of `-a` if you sign tags):

   ```sh
   git fetch && git tag -a v0.3.2 <commit> -m "ce 0.3.2" && git push origin v0.3.2
   ```

3. Approve the signing job. The job **Sign checksums.txt** waits for the environment `release`.
   Approve it on the run page (**Review deployments**), or:

   ```sh
   run=$(gh run list -R Checkend/checkend-cli -w Release -L 1 --json databaseId --jq '.[0].databaseId')
   gh api -X POST "repos/Checkend/checkend-cli/actions/runs/$run/pending_deployments" \
     -F "environment_ids[]=$(gh api repos/Checkend/checkend-cli/environments/release --jq .id)" \
     -f state=approved -f comment="v0.3.2"
   ```

   The job signs `checksums.txt` with the CLI release key, checks the signature with the keys of
   `install.sh`, uploads `checksums.txt.sig`, and publishes the draft release. Then the npm job
   publishes `@checkend/cli` (npm trusted publishing: no token), and the Homebrew job makes the
   formula.

   A job that failed on a network error (for example an HTTP 5xx while it downloads a tool) can
   run again: `gh run rerun <run> -R Checkend/checkend-cli --failed`. A job that failed on the
   code of the tag fails again: fix it on `main` and release the next patch version.

4. Check the release from outside:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/Checkend/checkend-cli/main/install.sh | CE_INSTALL_DIR="$(mktemp -d)" sh
   npx --prefer-online @checkend/cli@0.3.2 --version
   GOBIN="$(mktemp -d)" go install github.com/Checkend/checkend-cli/cmd/ce@v0.3.2
   ```

   `install.sh` prints `checksums.txt is signed by <key id>`. Each `--version` prints the version.

5. Update the Homebrew tap. The run summary has the `gh run download` command with the run ID.

   ```sh
   dir=$(mktemp -d)
   gh run download "$run" -R Checkend/checkend-cli -n homebrew-formula -D "$dir/f"
   git clone git@github.com:Checkend/homebrew-tap.git "$dir/tap"
   cp "$dir/f/checkend.rb" "$dir/tap/Formula/checkend.rb"
   git -C "$dir/tap" commit -am "Update ce to 0.3.2" && git -C "$dir/tap" push
   brew update && brew upgrade checkend/tap/checkend && ce --version
   ```

   The formula has the SHA-256 of each archive from `checksums.txt`, which the signing job checked.
