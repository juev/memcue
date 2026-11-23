# Releasing

One stable tag releases the binary and the npm plugins. The Git-distributed plugins and the npm packages still carry the same checked-in release version: installers use it to select a binary release.

## One-time npm setup

Before the first release using this workflow, add `release.yml` as a [trusted publisher](https://docs.npmjs.com/trusted-publishers/) for `memcue-dsh`, `memcue-pi` and `memcue-opencode` on npmjs.com. Use owner `juev`, repository `memcue`, no environment, and allow direct `npm publish`. Keep the old publishers until any release using the previous workflow has finished. No npm token is needed.

The old `memcue-transport` versions stay on npm for old installations. New plugin tarballs carry the transport from the same checkout, so no new standalone transport release is needed.

## Order

1. Update the version in a normal PR:
   - `package.json` (the repository root is `memcue-dsh`);
   - `plugins/transport/package.json` (the shared, in-tree transport);
   - `plugins/pi/package.json` and `plugins/opencode/package.json` with their matching lockfile metadata;
   - `plugins/claude-code/.claude-plugin/plugin.json` and `plugins/codex/.codex-plugin/plugin.json`;
   - `plugins/hermes/memcue/plugin.yaml` and `plugins/hermes/memcue-context/plugin.yaml`.
2. Update [Compatibility](../README.md#compatibility) and [Upgrade](../README.md#upgrade) with user-visible changes and any schema or flag changes. Merge the PR after normal CI passes. To rehearse npm packaging without publishing, use `make test-npm-packages`, the same command CI and the release job use.
3. Tag the exact verified merge commit and push only that tag:

   ```bash
   git tag vX.Y.Z <merge-commit>
   git push origin vX.Y.Z
   ```

   `release.yml` checks the tag against the manifests, runs the tests, saves the tested npm tarballs, and publishes the binaries and `checksums.txt` with GoReleaser. Then it publishes those same tarballs to npm. Do not move the tag or create additional plugin tags.
4. Check `gh release view vX.Y.Z` for the archives and `checksums.txt`. Verify each exact npm version (`npm view memcue-dsh@X.Y.Z version`, and likewise for `memcue-pi` and `memcue-opencode`) and verify `latest` points to `X.Y.Z` for a current stable release. If only npm jobs failed, re-run **failed jobs** on the original tag run. They accept an existing version only when its registry integrity matches the tested tarball.

## After the release

Upgrade every host that runs memcue, then check that the base opens and that search, the session-start note, a write with
`memcue add` and the plugins work. After a plugin update, check the binary separately: a plugin that downloads its own
release does so only when no configured binary is set.

- **Claude Code:** update the `juev/memcue` marketplace and the plugin from `/plugin`.
- **dsh:** installed plugins do not update (dsh 0.2.0-rc.2 says so in its plugin manager). Uninstall `memcue-dsh` and
  install it again. A profile owned by the desktop app is changed only from its **Plugins** page.
- **Hermes:** copy `plugins/hermes/memcue` over `$HERMES_HOME/plugins/memcue` again.
- **Pi:** `pi install npm:memcue-pi` again, or reload the installed package.
- **OpenCode:** `opencode plugin update memcue-opencode`, then restart the session; `/memcue status` names the binary.
