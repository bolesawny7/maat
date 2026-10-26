# Releasing

## A GitHub release

1. Check that CI is green on `main`.
2. Optionally, do a dry run: on GitHub, open Actions → Release → Run workflow. It builds both snaps and `maatctl` and attaches them to the run without publishing anything.
3. Tag the commit and push the tag:

   ```bash
   git tag -a v0.1.0 -m "maat 0.1.0"
   git push origin v0.1.0
   ```

The [Release workflow](../.github/workflows/release.yml) writes the version (the tag without its `v`) to `VERSION`, so the snap and both programs report it. Then it creates the GitHub release with `maat_amd64.snap`, `maat_arm64.snap`, `maatctl_linux_amd64`, `maatctl_linux_arm64` and `SHA256SUMS`. The asset names stay the same from one version to the next, because the README's install commands download them from the latest release.

The snap's grade is `devel`, so the Snap Store accepts it on the edge and beta channels only. That is deliberate while maat is a developer preview.

## The Snap Store

Once:

1. Sign in to [snapcraft.io](https://snapcraft.io) with an Ubuntu One account, then run `snapcraft login` on an Ubuntu machine.
2. Register the name: `snapcraft register maat`.
3. Export a credential that can only upload maat and release it to edge, and save it as the repository secret `SNAPCRAFT_STORE_CREDENTIALS` (Settings → Secrets and variables → Actions):

   ```bash
   snapcraft export-login --snaps=maat --channels=edge \
     --acls=package_access,package_push,package_update,package_release maat-store-login
   ```

   Paste the file's contents into the secret, then delete the file. The credential expires; export a new one when uploads start failing.

From then on, each tag also uploads both snaps to edge. To upload by hand instead, for example to watch the Store's automated review the first time, download the snaps from the GitHub release and run `snapcraft upload --release=edge maat_amd64.snap`, then the same for arm64.

maat's three interfaces are manual-connect, so people still connect them after installing from the Store:

```bash
sudo snap install maat --edge
for iface in log-observe system-observe mount-observe; do
  sudo snap connect "maat:$iface"
done
```

When the snap is on edge, switch the README's install commands to the Store. Connecting the interfaces automatically on install requires the Store's approval; ask in the `store-requests` category of [the Snapcraft forum](https://forum.snapcraft.io).
