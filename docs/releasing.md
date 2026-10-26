# Releasing

One release covers the binary and all three plugins: they carry the same version.

## Order

1. Open an issue for the release, then a PR for the version bump. A merged task does not ship until this PR does.
2. In that PR, set the new version in:
   - `package.json` (the repository-root dsh bundle);
   - `plugins/deepseek-harness/package.json`;
   - `plugins/claude-code/.claude-plugin/plugin.json`;
   - `plugins/hermes/memcue/plugin.yaml`;
   - `plugins/claude-code/test/versions.test.mjs`, which checks the four above and the README.
3. In `README.md`, update the release named in [Compatibility](../README.md#compatibility), add a row to its table, and
   add a section under [Upgrade](../README.md#upgrade): what changed for a user, and whether the schema or a flag did.
4. Run `make test`. After CI passes, merge the PR with a rebase.
5. Tag the merge commit twice and push both tags:

   ```bash
   git tag vX.Y.Z
   git tag dsh-vX.Y.Z
   git push origin vX.Y.Z dsh-vX.Y.Z
   ```

   `v*` runs `release.yml`: tests, then GoReleaser publishes the binaries and `checksums.txt`. `dsh-v*` runs
   `publish-dsh.yml`, which publishes `memcue-dsh` to npm and refuses a tag that differs from the package version.
   Without the `dsh-v` tag npm keeps the previous plugin, and that is how npm stayed at 0.5.3 through 0.11.1.
6. Check both results: `gh release view vX.Y.Z` lists the archives, and `npm view memcue-dsh version` prints `X.Y.Z`.

## After the release

Upgrade every host that runs memcue, then check that the base opens and that search, the session card, a write with
`memcue add` and the plugins work. After a plugin update, check the binary separately: a plugin that downloads its own
release does so only when no configured binary is set.

- **Claude Code:** update the `juev/memcue` marketplace and the plugin from `/plugin`.
- **dsh:** installed plugins do not update (dsh 0.2.0-rc.2 says so in its plugin manager). Uninstall `memcue-dsh` and
  install it again. A profile owned by the desktop app is changed only from its **Plugins** page.
- **Hermes:** copy `plugins/hermes/memcue` over `$HERMES_HOME/plugins/memcue` again.
