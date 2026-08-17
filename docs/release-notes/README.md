# Hand-written release notes

`release.yml` looks for `docs/release-notes/<tag>.md` (for example `v2.3.0.md`) and puts its contents
above the changelog git-cliff generates from the commits. Use it when a release needs a summary,
upgrade steps or a warning that a commit list cannot carry; skip it and the release notes are just the
grouped commit list.

The generated changelog is always appended below the intro — the file adds context, it never replaces
or filters what actually landed in the tag.
