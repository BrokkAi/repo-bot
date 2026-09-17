#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

tag=${1:?usage: bash scripts/release.sh vX.Y.Z}
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || exit 1
version=${tag#v}
repo=BrokkAi/repo-bot
dist=$(mktemp -d)
trap 'rm -rf "$dist"' EXIT
npm_tag=latest
release_flags=()
if [[ "$version" == *-* ]]; then
    npm_tag=next
    release_flags+=(--prerelease)
fi

python3 scripts/notices.py "$dist/THIRD_PARTY_NOTICES.txt"

gh release create "$tag" --repo "$repo" --verify-tag --generate-notes "${release_flags[@]}"
test -n "$(gh release view "$tag" --repo "$repo" --json url --jq .url)"

for os in linux darwin; do
    for arch in amd64 arm64; do
        cpu=$arch
        [[ "$arch" != amd64 ]] || cpu=x64
        package="$dist/$os-$cpu"
        mkdir -p "$package/bin"
        CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
            -ldflags "-s -w -X main.version=$tag" -o "$package/bin/brp" ./cmd/brp
        cp LICENSE NOTICE "$package/"
        cp "$dist/THIRD_PARTY_NOTICES.txt" "$package/"
        node - "$package" "$version" "$os" "$cpu" <<'JS'
const fs = require('node:fs');
const [dir, version, os, cpu] = process.argv.slice(2);
fs.writeFileSync(`${dir}/package.json`, JSON.stringify({
  name: `@brokkai/repo-bot-${os}-${cpu}`, version,
  os: [os], cpu: [cpu], license: 'MIT',
  repository: 'github:BrokkAi/repo-bot',
  files: ['bin', 'LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt']
}, null, 2));
JS
        asset="brokk-repo-bot-$tag-$os-$arch.tar.gz"
        tar -czf "$dist/$asset" -C "$package/bin" brp -C "$package" LICENSE NOTICE THIRD_PARTY_NOTICES.txt
        (cd "$dist" && shasum -a 256 "$asset") >> "$dist/checksums.txt"
        gh release upload "$tag" "$dist/$asset" --repo "$repo"
    done
done
gh release upload "$tag" "$dist/checksums.txt" --repo "$repo"
test "$(gh release view "$tag" --repo "$repo" --json assets --jq '.assets | length')" = 5

for os in linux darwin; do
    for cpu in x64 arm64; do
        (cd "$dist/$os-$cpu" && npm publish --access public --tag "$npm_tag")
    done
done

mkdir -p "$dist/launcher"
cp npm/brp.cjs LICENSE NOTICE README.md "$dist/launcher/"
cp "$dist/THIRD_PARTY_NOTICES.txt" "$dist/launcher/"
node - "$dist/launcher" "$version" <<'JS'
const fs = require('node:fs');
const [dir, version] = process.argv.slice(2);
const optionalDependencies = {};
for (const os of ['linux', 'darwin']) {
  for (const cpu of ['x64', 'arm64']) {
    optionalDependencies[`@brokkai/repo-bot-${os}-${cpu}`] = version;
  }
}
fs.writeFileSync(`${dir}/package.json`, JSON.stringify({
  name: '@brokkai/repo-bot', version, license: 'MIT',
  repository: 'github:BrokkAi/repo-bot',
  bin: {brp: 'brp.cjs'}, files: ['brp.cjs', 'LICENSE', 'NOTICE', 'README.md', 'THIRD_PARTY_NOTICES.txt'],
  optionalDependencies
}, null, 2));
JS
(cd "$dist/launcher" && npm publish --access public --tag "$npm_tag")
