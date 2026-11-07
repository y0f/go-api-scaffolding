# Release workflow

The tag-triggered [workflow](workflows/release.yml) uses pinned GoReleaser,
cosign, Syft, and GitHub Actions. Release coordination uses Python's standard
library on the hosted runner; normal application development still uses Go.

## Publication order

1. Resolve the remote version tag and require it to match the checked-out
   commit. An existing published immutable release selects verification only;
   an existing published mutable release stops the workflow.
2. Build the archives, SBOMs, and signed checksum manifest. GoReleaser uploads
   them to a draft. Reruns can replace assets while that release is a draft.
3. For public GitHub.com repositories, generate build provenance attestations.
4. Download every uploaded asset into a fresh directory. Require the exact
   inventory from GoReleaser's artifact metadata, compare bytes with the local
   build and GitHub's digests, and check complete SHA-256 manifest coverage.
   Verify the checksum signature against this workflow, tag, and source commit;
   verify public build provenance against the same source identity.
5. Recheck the tag and draft asset metadata, then publish. If the resulting
   release is immutable, verify GitHub's release attestation and every asset.

The checks derive names from the build; initialized projects can use their own
module and release project names. If you add another GoReleaser artifact type
or change checksum/signature naming, update the verifier and its tests.

## Enable immutable releases

Release immutability is a repository setting, not a setting copied by
`forge init`. Enable it in the repository's Settings before publishing a version
that you want protected. It locks future published release tags and uploaded
assets; earlier releases and arbitrary branch references remain mutable.
See GitHub's [setup instructions](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/establish-provenance-and-integrity/prevent-release-changes)
and [immutability guarantees](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases).

The workflow also supports repositories without that setting. It reports when
the published release is mutable and does not claim a GitHub release-attestation
check ran. Public build provenance remains separate from release immutability.

## Failures and reruns

A failure before publication leaves a draft. Investigate the failure and rerun
the workflow; unexpected extra draft assets must be reviewed and removed before
retrying. Verification never silently ignores an extra asset.

A failure after publication can leave an already-published release. With
immutability enabled, rerunning verifies the existing assets without rebuilding,
uploading, or replacing them. It does not repair a broken published artifact;
that needs a new version. GitHub's release attestation is generated at
publication, so its verification necessarily follows that step.

For a published mutable release, the workflow refuses to overwrite it. Review
the existing release and use a new version for changes. Do not move a release
tag to a different commit to repair a workflow failure.

## Local verification

```sh
python -m unittest discover -s .github/scripts -p 'test_*.py'
goreleaser release --snapshot --clean --skip=publish,sign
python .github/scripts/release.py local --unsigned-snapshot
```

Use the GoReleaser and Syft versions from the workflow. The local command checks
the generated inventory and file hashes. It cannot establish GitHub OIDC signing,
uploaded bytes, hosted provenance, or release immutability; those checks run in
the release workflow. See [consumer verification](../README.md#verify-a-release).
