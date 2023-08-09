#!/bin/bash


version_string=$1
VERSION=${version_string#v}
ARCH=$2

echo "Building border0 ${VERSION} for ${ARCH}"

mkdir -p border0_${VERSION}_${ARCH}/usr/
bin
mkdir -p border0_${VERSION}_${ARCH}/etc/border0
mkdir -p border0_${VERSION}_${ARCH}/DEBIAN

cp mysocketctl_linux_${ARCH} border0_${VERSION}_${ARCH}/usr/
bin/border0

echo """
Package: border0
Version: ${VERSION}
Section: base
Priority: optional
Architecture: ${ARCH}
Maintainer: Greg Duraj <greg@border0.com>
Description: Border0 Connector and CLI tooling
""" > border0_${VERSION}_${ARCH}/DEBIAN/control

chmod -R 755 border0_${VERSION}_${ARCH}
chmod 644 border0_${VERSION}_${ARCH}/DEBIAN/control
chmod 755 border0_${VERSION}_${ARCH}/usr/
bin/border0

dpkg-deb -Zxz --build border0_${VERSION}_${ARCH}

rm -fr border0_${VERSION}_${ARCH}