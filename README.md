border0: a CLI tool for Border0.com
===================================================

[![Run tests](https://github.com/borderzero/border0-cli/actions/workflows/run_tests.yml/badge.svg)](https://github.com/borderzero/border0-cli/actions/workflows/run_tests.yml)

border0 is a CLI tool for interacting with https://border0.com and a wrapper around the [border0.com API](https://api.border0.com/).

Please check the full documentation here: [https://docs.border0.com/](https://docs.border0.com/docs)

Installation
--------------------
For DEB based Linux distributions (Debian, Ubuntu, etc):

Add the Border0 GPG key to your system
```
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://border0-dev-rnd-bucket.s3.us-east-2.amazonaws.com/repos/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/border0.gpg
```

Add the repository to your sources list
```
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/border0.gpg] https://border0-dev-rnd-bucket.s3.us-east-2.amazonaws.com/repos/ stable main" | sudo tee /etc/apt/sources.list.d/border0.list
```

Install the border0 package
```
sudo apt-get update
sudo apt-get install border0
```

Binary releases can be found at https://download.border0.com


Shell auto-completion
--------------------
display autocomplete installation instructions
```shell
border0 completion --help
```
Working with Docker
--------------------
We publish docker image alongside our binary toolkit release, you can pull it from GitHub registry:
```shell
docker pull ghcr.io/borderzero/border0
```
Great! we are now ready to run some commands and login

### Authentication and cache directory
Our toolkit caches tokens and config files in `.border0` directory under User's HOME path ($HOME/.border0)

In case you cannot download/run border0 binary from https://download.border0.com and docker image is your only option. You can use volumes for persistent storage and handle the `$HOME/.border0` across your containers:

First of all, in the home path of the user we create our cache directory ``mkdir .border0`` (you can use any other name and path, but using $HOME/.border0 keeps it compatible with border0 binary and makes it way easier to start with)

We can then login as Administrator persona to our Organization using our docker image. We preserve the authentication tokens by passing/mounting the `.border0` directory we just created.
```bash
docker run -ti --rm -v ~/.border0:/root/.border0:rw \
 ghcr.io/borderzero/border0 login

Please navigate to the URL below in order to complete the login process:
https://portal.border0.com/login?device_identifier=IjZiYmJjMTkwLTBkNDktNGNmYi05NzMyLWZhY2FjMDM5NDVjYiI.ZxIdzE.61HPzXmOuH7ezyLQlG3RuFAMQS0

```
From now on we can either keep using the volume or alternatively we can read the token into `BORDER0_ADMIN_TOKEN` environment variable and pass the authentication credentials that way

### Using Tokens
At this point we have only been using temporary tokens via the ``border0 login`` function

We have a whole section on creating and managing permanent tokens here: [Creating API Tokens](https://docs.border0.com/docs/creating-access-token). Please take some time to explore token functionality via our [Admin Portal](https://portal.border0.com/organizations/current?tab=tokens)

We recommend the usage of persistent tokens, you can pass them into the docker container in 2 ways:
As a volume we already mentioned, place your token in the `$HOME/.border0/token` file
Or as `BORDER0_ADMIN_TOKEN` environment variable

Below we have examples of using the directory volume, and environment variable to achieve the same goal
```bash
# env variable way
docker run -ti --rm --env BORDER0_ADMIN_TOKEN=$(cat ~/.border0/token) \
 ghcr.io/borderzero/border0 account show

# volume way
docker run -ti --rm -v ~/.border0:/root/.border0:rw \
 ghcr.io/borderzero/border0 account show

```
Commands abo achieve the same outcome but provide flexibility in handling credentials.
### Connector

The Connector functionality can be invoked with `border0 connector start` function and requires a Yaml config file (`border0.yaml` by default)

At the very least `border0.yaml` needs to have connector name defined:
```yaml
connector:
   name: "my-connector"
```

We will use docker --mount option to pass our yaml config to the container, as well as `BORDER0_ADMIN_TOKEN` variable containing our admin token
```bash
docker run -ti --rm --network=host \
--mount type=bind,source=./border0.yaml,target=/border0.yaml,readonly \
--env BORDER0_ADMIN_TOKEN=$(cat ~/.border0/token) \
 ghcr.io/borderzero/border0 connector start
```

## End-Users Accessing Border0 Sockets
The end users are authenticated in a separate flow and are issued individual temporary credentials.

Generic Socket clients can login to the platform with `border client login --org=MyOrgName` (your Organization name is what comes before .border0.io: `MyOrgName.border0.io`)

```bash
docker run -ti --rm -v ~/.border0:/root/.border0:rw \
 ghcr.io/borderzero/border0 client login --org=MyOrgName

Please navigate to the URL below in order to complete the login process:
https://api.border0.com/api/v1/client/auth/org/MyOrgName?device_identifier=IjI5MGQ0NjIxLTJlOGUtNGQ5MS1iNTcxLTNlYzJmZWI4OTQzOSI.Z4IsbB.3FgOaPbV3sXsqh3DqIplEMIBd4A
```
As we have seen above the client credentials (or token) is cached under `$HOME/.border0/client_token`

Once we've obtained client token we can pass it to our containers the same way as admin tokens
```bash
# env variable way
docker run -ti --rm --env BORDER0_CLIENT_TOKEN=$(cat ~/.border0/client_token) \
 ghcr.io/borderzero/border0 client hosts

#volume way
docker run -ti --rm ~/.border0:/root/.border0:rw \
 ghcr.io/borderzero/border0 client hosts

```

Security
--------------------
Please go [here](SECURITY.md) for reporting security concerns
