# Development environment for `kubic-init`

## Project structure

This project follows the conventions presented in the [standard Golang
project](https://github.com/golang-standards/project-layout).

## Dependencies

* `dep` (will be installed automatically if not detected)
* `go >= 1.10`

For running the `kubic-init` (either locally, in a container or in a Terraform
deployment) please make sure the `kubelet` version running in the host system
is the same `kubic-init` was compiled against. You can check current kubernetes
version in the [Gopkg.toml requirements file](../Gopkg.toml)..

### Bumping the Kubernetes version used by `kubic-init`

Update the constraints in [`Gopkg.toml`](../Gopkg.toml).

## Building

A simple `make` should be enough. This should compile [the main
function](../cmd/kubic-init/main.go) and generate a `kubic-init` binary as
well as a _Docker_ image.

## Running `kubic-init` in your Development Environment

There are multiple ways you can run the `kubic-init` for bootstrapping
and managinig your Kubernetes cluster:

### ... in your local machine

You can run the `kubic-init` container locally with a
`make docker-run`. This will:

  * build the `kubic-init` image
  * install a [_drop-in_](../init/kubelet.drop-in.conf) unit for
  kubelet, so it can be started with the right parameters.
  * stop the `kubelet`
  * run it with `docker`
    * using the config files in [`../config`](`../config`)
    * mounting many local directories in the containar (so
    please review the `CONTAINER_VOLUMES` in the [`Makefile`](../Makefile))
  * start the `kubelet`
  * start all the control-plane containers (etcd, the API server,
  the controller manager and the scheduller) in the local
  `docker` daemon.

Once you are done, you can `make docker-reset` for stopping the
control plane and removing all the leftovers.

### ... with Terraform

You can run an instance locally (as we described in the previous section)
and and then use this instance as a _seeder_ for new nodes that are
started in VMs with the help of Terraform. You can start these nodes with a
`make tf-nodes-run`. This will:

  * start Kubic-based VMs, generating some config files from
  the [`cloud-init` templates](../deployments/cloud-init)
  * copy some config files and drop-in units, install packages, etc...
  * copy the `kubic-init:latest` image and load it in the CRI.
  * start the `kubic-init` container from a CRI _systemd_ unit.

Do a `make tf-nodes-destroy` once you are done.
See the [`../deployments/tf-libvirt-nodes`](../deployments/tf-libvirt-nodes)
directory for more details.

If you prefer to run all the Kubic machines with terraform (both the
_seeder_ and the _nodes_) you can run the cluster with `make tf-full-run`.

