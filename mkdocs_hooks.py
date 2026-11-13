"""MkDocs hooks. doc/index.md is a symlink to README.md, whose links point
into doc/ from the repo root; rewrite them to be relative to doc/."""

import re

_DOC_LINK = re.compile(r"\]\(doc/")
_SITE_LINE = re.compile(r"^Documentation: https://binarycat0\.github\.io/watchcat/?\n\n?", re.M)


def on_page_markdown(markdown, page, **kwargs):
    if page.file.src_uri != "index.md":
        return markdown
    # Edit the real file, not the symlink.
    page.edit_url = "https://github.com/binarycat0/watchcat/edit/main/README.md"
    markdown = _SITE_LINE.sub("", markdown)
    return _DOC_LINK.sub("](", markdown)
