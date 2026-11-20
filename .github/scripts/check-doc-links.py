#!/usr/bin/env python3
# SPDX-License-Identifier: LGPL-3.0-or-later
"""Checks the links of the built docs site that point into the site.

Usage: check-doc-links.py SITE_DIR HUGO_CONFIG

SITE_DIR is the directory that Hugo wrote the site into, HUGO_CONFIG the
hugo.toml whose baseURL the site has. For each <a href> of each page that
points into the site, the script checks that the page or file exists and,
for a link with a fragment, that the page has an element with that id. It
prints each broken link as <page>: <href>: <reason> and exits with 1 when
there is one. Links to other sites are not checked: they need the network.

Hugo fails the build for a relref to a page that does not exist. It does
not check the fragment of a relref, a link that is written as a path, or a
link of a template or a shortcode. This script does.
"""
import pathlib
import sys
import tomllib
import urllib.parse
from html.parser import HTMLParser


class Page(HTMLParser):
    """Collects the ids and the links of a page."""

    def __init__(self):
        super().__init__()
        self.ids = set()
        self.links = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        for key in ("id", "name"):
            if attrs.get(key):
                self.ids.add(attrs[key])
        if tag == "a" and attrs.get("href") is not None:
            self.links.append(attrs["href"])


def main(site_dir, config):
    site = pathlib.Path(site_dir)
    with open(config, "rb") as f:
        base = urllib.parse.urlsplit(tomllib.load(f)["baseURL"])
    root = base.path if base.path.endswith("/") else base.path + "/"

    pages = {}
    for path in sorted(site.rglob("*.html")):
        page = Page()
        page.feed(path.read_text(encoding="utf-8"))
        pages[path] = page

    def target(page_path, link):
        """Returns the file that link points to, None for another site."""
        if link.scheme or link.netloc:
            if (link.scheme, link.netloc) != (base.scheme, base.netloc):
                return None
        path = urllib.parse.unquote(link.path)
        if not path:
            return page_path
        if path.startswith("/"):
            if not (path + "/").startswith(root):
                return None  # another site of the same host
            file = site / path[len(root):]
        else:
            file = page_path.parent / path
        return file / "index.html" if file.is_dir() else file

    broken = []
    for page_path, page in pages.items():
        name = page_path.relative_to(site)
        for href in page.links:
            link = urllib.parse.urlsplit(href)
            file = target(page_path, link)
            if file is None:
                continue
            if not file.is_file():
                broken.append(f"{name}: {href}: no such page")
                continue
            fragment = urllib.parse.unquote(link.fragment)
            if fragment and file in pages and fragment not in pages[file].ids:
                broken.append(f"{name}: {href}: no element with the id {fragment}")

    for line in broken:
        print(line)
    links = sum(len(p.links) for p in pages.values())
    print(f"{len(pages)} pages, {links} links, {len(broken)} broken")
    return 1 if broken else 0


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    sys.exit(main(sys.argv[1], sys.argv[2]))
