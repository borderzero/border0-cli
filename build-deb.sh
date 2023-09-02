#!/bin/bash


version_string=$1
VERSION=${version_string#v}
ARCH=$2

echo "Building border0 ${VERSION} for ${ARCH}"

mkdir -p border0_${VERSION}_${ARCH}/usr/bin
mkdir -p border0_${VERSION}_${ARCH}/etc/border0
mkdir -p border0_${VERSION}_${ARCH}/DEBIAN

echo "Copying files..."
cp ./bin/border0_linux_${ARCH} border0_${VERSION}_${ARCH}/usr/bin/border0
cp -pvr DEBIAN/* border0_${VERSION}_${ARCH}/DEBIAN/

echo "Creating control file..."
echo """
Package: border0
Version: ${VERSION}
Section: base
Priority: optional
Architecture: ${ARCH}
Maintainer: Greg Duraj <greg@border0.com>
Description: Border0 Connector and CLI tooling
""" > border0_${VERSION}_${ARCH}/DEBIAN/control

echo "Setting permissions..."
chmod -R 755 border0_${VERSION}_${ARCH}
chmod 644 border0_${VERSION}_${ARCH}/DEBIAN/control
chmod 755 border0_${VERSION}_${ARCH}/DEBIAN/postinst
chmod 755 border0_${VERSION}_${ARCH}/DEBIAN/postrm
chmod 755 border0_${VERSION}_${ARCH}/usr/bin/border0

echo "Building package..."
dpkg-deb -Zxz --build border0_${VERSION}_${ARCH}

# echo "Cleaning up binaries..."
# rm -fr border0_${VERSION}_${ARCH}

echo "Copying package to repo..."
mkdir -p deb/pool/main/
cp border0_${VERSION}_${ARCH}.deb deb/pool/main/
mkdir -p deb/dists/stable/main/binary-${ARCH}

cd deb
dpkg-scanpackages --arch ${ARCH} pool/ > dists/stable/main/binary-${ARCH}/Packages
cat dists/stable/main/binary-${ARCH}/Packages | gzip -9 > dists/stable/main/binary-${ARCH}/Packages.gz
cd -

echo "Done!"
