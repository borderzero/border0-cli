#!/bin/bash


version_string=$1
VERSION=${version_string#v}
ARCH=$2

echo "Building border0 ${VERSION} for ${ARCH}"

mkdir -p border0-${VERSION}_${ARCH}/usr/local/bin
mkdir -p border0-${VERSION}_${ARCH}/etc/border0
mkdir -p border0-${VERSION}_${ARCH}/DEBIAN

cp mysocketctl_linux_${ARCH} border0-${VERSION}_${ARCH}/usr/local/bin/border0
echo -e "# template config file for ${ARCH} ver:${VERSION}" > border0-${VERSION}_${ARCH}/etc/border0/connector.yaml

echo """
Package: border0
Version: ${VERSION}
Section: base
Priority: optional
Architecture: ${ARCH}
Maintainer: Greg Duraj <greg@border0.com
Description: border0 is a CLI tool for interacting with https://border0.com and a wrapper around the border0.com API. Please check the full documentation here: https://docs.border0.com
""" > border0-${VERSION}_${ARCH}/DEBIAN/control

chmod -R 755 border0-${VERSION}_${ARCH}
chmod 644 border0-${VERSION}_${ARCH}/DEBIAN/control
chmod 755 border0-${VERSION}_${ARCH}/usr/local/bin/border0
chmod 644 border0-${VERSION}_${ARCH}/etc/border0/connector.yaml

dpkg-deb -Zxz --build border0-${VERSION}_${ARCH}

rm -fr border0-${VERSION}_${ARCH}