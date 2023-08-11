#!/bin/sh
set -e

cd repos/dists/stable

do_hash() {
    HASH_NAME=$1
    HASH_CMD=$2
    echo "${HASH_NAME}:"
    for f in $(find -type f); do
        f=$(echo $f | cut -c3-) # remove ./ prefix
        if [ "$f" = "Release" ]; then
            continue
        fi
        echo " $(${HASH_CMD} ${f}  | cut -d" " -f1) $(wc -c $f)"
    done
}

echo "Generating hashes..."
cat << EOF
Origin: Border0 Repository
Label: Border0
Suite: stable
Codename: stable
Version: 1.0
Architectures: amd64 arm64 armv6 arm
Components: main
Description: Border0 Connector and CLI tooling Repository
Date: $(date -Ru)
EOF > Release
do_hash "MD5Sum" "md5sum" >> Release 
do_hash "SHA1" "sha1sum" >> Release
do_hash "SHA256" "sha256sum" >> Release

# signing the release
echo "Signing the release..."
# cat /pgp-key.private | gpg --import # this needs to be imported in Actions
cat Release | gpg --default-key example -abs > Release.gpg
cat Release | gpg --default-key example -abs --clearsign > InRelease

echo "Done! All files are in repos/dists/stable/"