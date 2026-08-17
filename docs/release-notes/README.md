# Hand-written release notes

`release.yml` looks for `docs/release-notes/<tag>.md` (for example `v2.3.0.md`). When the file exists
it becomes the release body as is, and the changelog git-cliff would have generated from the commits is
skipped — a release that got a written summary does not also want the raw commit list under it. Without
the file the generated changelog is the whole body.

Either way the workflow appends a link to the diff against the previous tag.
