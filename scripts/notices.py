#!/usr/bin/env python3
"""Collect release notices from the selected Go modules and build toolchain."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
LEGAL = re.compile(r"^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS)([._-].*)?$", re.I)
UNICODE_URL = "https://www.unicode.org/license.txt"


def go(*args):
    return subprocess.check_output(
        ["go", *args], cwd=ROOT, text=True, timeout=120,
        env=dict(os.environ, GOWORK="off", GOFLAGS="-mod=readonly"),
    )


def objects(text):
    decoder = json.JSONDecoder()
    while text.strip():
        obj, end = decoder.raw_decode(text.lstrip())
        yield obj
        text = text.lstrip()[end:]


def legal_files(directory):
    return sorted(p for p in directory.rglob("*")
                  if p.is_file() and LEGAL.fullmatch(p.name))


def unicode_notice():
    with urllib.request.urlopen(UNICODE_URL, timeout=30) as response:
        text = response.read(1024 * 1024).decode("utf-8")
    if "UNICODE LICENSE" not in text or "COPYRIGHT AND PERMISSION NOTICE" not in text:
        raise ValueError("Unicode server did not return a license notice")
    return text


def generate():
    sections = ["THIRD-PARTY NOTICES\n\nGenerated for this release from its Go dependencies "
                "and toolchain.\nProject terms are in LICENSE and NOTICE.\n"]
    modules = list(objects(go("list", "-m", "-json", "all")))
    if any(m.get("Replace") for m in modules):
        raise ValueError("release notices require published modules, not replacements")
    for module in sorted(modules, key=lambda m: m["Path"]):
        if module.get("Main"):
            continue
        identity = module["Path"] + "@" + module["Version"]
        downloaded = json.loads(go("mod", "download", "-json", identity))
        directory = Path(downloaded["Dir"])
        files = legal_files(directory)
        if not any(p.name.upper().startswith(("LICENSE", "LICENCE", "COPYING")) for p in files):
            raise ValueError(f"no license text found for {identity}")
        sections.append(identity + "\n")
        for path in files:
            sections.append(f"--- {identity}/{path.relative_to(directory).as_posix()} ---\n\n"
                            + path.read_text(encoding="utf-8"))
    go("mod", "verify")
    toolchain = json.loads(go("env", "-json", "GOROOT", "GOVERSION"))
    goroot = Path(toolchain["GOROOT"])
    for name in ("LICENSE", "PATENTS"):
        path = toolchain_file(goroot, name)
        if name == "LICENSE" or path.exists():
            sections.append(f"Go runtime and standard library {toolchain['GOVERSION']}/{name}\n\n"
                            + path.read_text(encoding="utf-8"))
    sections.append(f"Unicode data used by Go and dependencies\nSource: {UNICODE_URL}\n\n"
                    + unicode_notice())
    return ("\n" + "=" * 78 + "\n\n").join(s.rstrip() + "\n" for s in sections)


def toolchain_file(goroot, name):
    """Locate one Go toolchain legal file.

    Most distributions keep them inside GOROOT. Homebrew installs the toolchain
    as the formula's libexec and leaves LICENSE in the directory above it, so a
    release built on a developer's machine must look there too rather than
    report the toolchain license as missing.
    """
    path = goroot / name
    if path.exists():
        return path
    beside = goroot.parent / name
    if beside.exists():
        return beside
    return path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path, help="release staging path for THIRD_PARTY_NOTICES.txt")
    args = parser.parse_args()
    try:
        report = generate()  # Complete collection before creating the artifact.
        args.output.write_text(report, encoding="utf-8")
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f"Could not generate release notices: {error}\n")


if __name__ == "__main__":
    main()
