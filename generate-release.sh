#!/bin/sh
set -e
apt-ftparchive -c release.conf release repos/dists/stable > repos/dists/stable/Release

# signing the release
echo "Signing the release..."
# cat /pgp-key.private | gpg --import # this needs to be imported in Actions
cat repos/dists/stable/Release | gpg --default-key -abs > repos/dists/stable/Release.gpg
cat repos/dists/stable/Release | gpg --default-key -abs --clearsign > repos/dists/stable/InRelease

echo "Generating public key..."
gpg --armor --output repos/gpg

echo -e "Done!\nAll files are in repos/dists/stable/\n"
