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

The `--allow-publish` permission is npm's `createPackage`, so the workflow may
publish a package's first version: configure the publisher before the first
tag and no version is ever published by hand. If the registry refuses a name it
has never seen, publish that version once from a terminal with
`bash scripts/release.sh vX.Y.Z` and configure trust afterwards.

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
