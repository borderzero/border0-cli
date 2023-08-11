#!/bin/bash
echo "%echo Generating border0 PGP key
Key-Type: RSA
Key-Length: 4096
Name-Real: border0
Name-Email: support@border0.com
Expire-Date: 0
%no-ask-passphrase
%no-protection
%commit" > border0-pgp-key.batch
gpg --no-tty --batch --gen-key border0-pgp-key.batch
gpg --armor --export border0 > border0-pgp-key.public
gpg --armor --export-secret-keys border0 > border0-pgp-key.private

echo -e "Done! Your PGP key is in border0-pgp-key.public and border0-pgp-key.private/n"
