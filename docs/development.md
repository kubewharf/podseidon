# Development guide

Most development-related tasks in the Podseidon project
are executed using [earthly](https://earthly.dev).

For compatibility with private Git replaces under some environments,
all Go module downloading commands use `RUN --ssh`, which requires an ssh-agent socket.
However, no special SSH access is required for the default setup.
Simply run all `earthly` commands with an `ssh-agent`
to workaround the "no SSH key forwarded from the client" error.

The Earthly targets for Podseidon perform all operations
(except `go mod tidy`) inside Docker containers.
Thus, system settings are not respected by default.
For users behind a firewall, override the corresponding build arguments,
e.g. `earthly +target --golang_image='your-container-registry.com/golang:1.23-alpine3.20`.
<code>grep '^ *ARG' [Earthfile](../Earthfile)</code> for a list of available arguments.

## Building

Simply run `ssh-agent earthly +build`.
This builds the binaries and docker images for each of the three components,
plus a binary for the `allinone` package.
Artifact paths are indicated in the `earthly` output.

## Pre-commit checks

The following steps are advised before committing:

```shell
git add -A    # stage all changes to before formatting
earthly +fmt  # reformat code and reorganize imports
earthly +lint # perform static analysis
earthly +test # run fast unit tests
earthly -P +e2e  # run slow e2e tests
```

Note that `+e2e` creates a docker-in-docker container
and pulls/loads 13 docker images into the guest dockerd,
running 26 docker containers inside.
This may be resource-consuming for your system.

To debug e2e tests interactively,
run the following command instead:

```shell
earthly -iP +e2e --FAIL_FAST_FOR_INTERACTIVE=yes
```

If e2e test cases fail, this will open an interactive shell inside the e2e setup.
Omitting `FAIL_FAST_FOR_INTERACTIVE=yes` would result in the interactive shell
spawning after the DinD daemon is stopped.

[Kelemetry](https://github.com/kubewharf/kelemetry) traces are exported to /var/trace/outputs.

## Code style

We use [editorconfig](https://editorconfig.org) to standardize project code style.
Installing the corresponding plugin for your editors is recommended.

### Go code

Go code style is regulated by `gofumpt`, `gci` and some other `golangci-lint` linters.
`earthly +fmt && earthly +lint` output provides suggestions to meet the code style requirements.

### Generated code

All generated code must be marked as `linguist-generated=true` in [.gitattributes](../.gitattributes).

### Helm chart

Podseidon has a complex chart setup to maximize reusability in different environments.
Thus, there are some non-standard rules as specified in [STYLE.md](../chart/STYLE.md).

## Contact

If you have any questions, don't hesitate to reach out on
[GitHub discussions](https://github.com/kubewharf/podseidon/discussions).
