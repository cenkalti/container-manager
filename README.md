# container-manager

container-manager is a daemon that watches your containers. It reads container definitions from a config file and creates/removes containers, similar to `docker swarm deploy` command but does not require Docker daemon to be in swarm mode.

## Installing

Install latest released binary from [releases page](https://github.com/cenkalti/container-manager/releases) or install development version with:

```
go get github.com/cenkalti/container-manager
```

If you're using Arch, you can install `container-manager-bin` package from AUR.

## Usage

- Put your container definitions in a config file.
- Run `contianer-manager -config <path>` with systemd, supervisor or etc.
- Update configuration when you are about to deploy/update containers. (Do not forget to change the `version` field.)
- Reload configuration with `SIGHUP`. container-manager will update running containers.

## Example config

```yaml
checkinterval: 60s
listenaddr: 0.0.0.0:26662
containers:
  nginx:
    version: 1
    image: nginx
    portbindings:
      "80/tcp":
        - hostip: "0.0.0.0"
          hostport: 8080
  sleep:
    version: 1
    count: 4
    stoptimeout: 5
    image: ubuntu:16.04
    cmd:
      - bash
      - "-c"
      - sleep 999999999
```

## Notes

### Private repositories

If you are using a private Docker repository, you will need credentials to pull new images.
container-manager uses the same config file (`~/.docker/config`) as the docker CLI tool.

### Container logs

Because running containers are removed and new containers are created when they get redeployed,
log messages will also be deleted if you use `json-file` log driver (default).
It is recommended to use another log-driver to keep your log messages after containers are deleted.

Example:
```yaml
containers:
  ticker:
    image: ubuntu:16.04
    cmd:
      - bash
      - "-c"
      - while true; do date; sleep 1; done
    logconfig:
      type: journald
```

If you use `journald` log driver, you can access logs with following command:

```
journalctl CONTAINER_NAME=ticker
```
