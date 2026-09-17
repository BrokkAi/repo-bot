# Releasing repo-bot

Push a version tag on the commit to release:

```sh
git tag v0.2.2
git push origin v0.2.2
```

The tag workflow runs `scripts/release.sh`: create the GitHub release, build and
upload Linux/macOS amd64/arm64 archives and installer checksums, then publish the
four native npm packages and the launcher. Versions with a prerelease suffix use
npm's `next` tag and GitHub's prerelease flag.

npm uses trusted publishing for `publish-packages.yml` in the `packages-publish`
environment. All five packages need that publisher configured, once, from a
terminal that can answer npm's two-factor prompt:

```sh
for package in repo-bot repo-bot-linux-x64 repo-bot-linux-arm64 \
    repo-bot-darwin-x64 repo-bot-darwin-arm64; do
    npm trust github "@brokkai/$package" --file publish-packages.yml \
        --repo BrokkAi/repo-bot --env packages-publish --allow-publish -y
done
npm trust list @brokkai/repo-bot
```

All five packages were bootstrapped at `0.1.0` on 2026-09-17 and their trusted
publishers were verified. `--allow-publish` enables `createPackage`; the registry
also returned `createStagedPackage`. The registry rejected trust configuration
for an unpublished name with `E404 Package not found`, so the first versions
were published locally and have no provenance attestations. Future version tags
use the configured GitHub publisher.

For a new package name, try configuring trust first. If npm rejects the unknown
name, bootstrap from an authenticated interactive terminal. Temporarily disable
`publish-packages.yml` and verify it is disabled before pushing the version tag:
`scripts/release.sh` requires a remote tag, and an active workflow would race the
local release. Run `bash scripts/release.sh vX.Y.Z`, configure and verify trust
for every package, then re-enable the workflow. Do not rerun the whole release
script after partial success: inspect existing release assets and npm versions
first. npm browser verification requires a TTY; press Enter and complete the
browser flow without copying credentials or authentication URLs into logs.

Before creating the GitHub release, packaging runs `scripts/notices.py` once.
It collects license, notice, copying, and patent files from the selected Go
module versions (including nested files), the build toolchain's license and
patent grant, and the Unicode license. The generated `THIRD_PARTY_NOTICES.txt`
is included in every native archive and npm package. Nothing is generated into
the source tree and there is no license approval inventory to maintain.

Generation needs access to the Go module proxy and unicode.org. If collection
fails, the release stops before publishing; fix the download or missing upstream
license and rerun. This collects published notices, rather than classifying
licenses or auditing arbitrary embedded third-party material.

Local checks use fake dependencies and responses and never publish:

```sh
python3 -m unittest discover -s scripts -p '*_test.py'
node --test npm/brp.test.cjs
bash -n scripts/release.sh
```
