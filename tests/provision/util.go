// Copyright 2025 The Podseidon Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provision

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/optional"
	"github.com/kubewharf/podseidon/util/util"
)

var nextCommandId atomic.Uint32

func (setup *Setup) Command(command string, args ...string) *Command {
	idInt := nextCommandId.Add(1)
	idBuf := new(bytes.Buffer)
	_ = binary.Write(hex.NewEncoder(idBuf), binary.BigEndian, idInt) // bytes.Buffer is infallible
	id := idBuf.String()

	cmd := &Command{
		logger:     setup.Run.Logger.WithValues("execId", id),
		setup:      setup,
		command:    command,
		args:       args,
		additional: nil,
		done:       make(chan util.Empty),
	}
	cmd.WorkDir(setup.Paths.Temp)
	cmd.Envs(os.Environ())
	cmd.additional = append(cmd.additional, func(c *exec.Cmd) error {
		c.Stdout = bufferedReadLog(cmd.logger.WithValues("fd", "stdout"), cmd.done)
		c.Stderr = bufferedReadLog(cmd.logger.WithValues("fd", "stderr"), cmd.done)
		return nil
	})
	return cmd
}

type Command struct {
	logger logr.Logger
	setup  *Setup

	command string
	args    []string

	additional []func(*exec.Cmd) error

	done chan util.Empty
}

func (cmd *Command) MoreArgs(args ...string) *Command {
	cmd.args = append(cmd.args, args...)
	return cmd
}

func (cmd *Command) Kubeconfig(clusterId ClusterId) *Command {
	return cmd.KubeconfigPath(cmd.setup.Env.Clusters[clusterId.Index()].HostKubeconfigPath)
}

func (cmd *Command) KubeconfigPath(kubeconfigPath string) *Command {
	return cmd.Env("KUBECONFIG", kubeconfigPath)
}

func (cmd *Command) Env(key string, value string) *Command {
	return cmd.Envs([]string{fmt.Sprintf("%s=%s", key, value)})
}

func (cmd *Command) Envs(envs []string) *Command {
	cmd.additional = append(cmd.additional, func(c *exec.Cmd) error {
		c.Env = append(c.Env, envs...)
		return nil
	})
	return cmd
}

func (cmd *Command) WorkDir(workDir string) *Command {
	cmd.additional = append(cmd.additional, func(c *exec.Cmd) error {
		c.Dir = workDir
		return nil
	})
	return cmd
}

func (cmd *Command) StdoutFile(outPath string) *Command {
	cmd.additional = append(cmd.additional, func(c *exec.Cmd) error {
		fd, err := os.Create(outPath)
		if err != nil {
			return fmt.Errorf("open %s for writing command output: %w", outPath, err)
		}
		c.Stdout = fd
		return nil
	})
	return cmd
}

func (cmd *Command) StderrFile(outPath string) *Command {
	cmd.additional = append(cmd.additional, func(c *exec.Cmd) error {
		fd, err := os.Create(outPath)
		if err != nil {
			return fmt.Errorf("open %s for writing command output: %w", outPath, err)
		}
		c.Stderr = fd
		return nil
	})
	return cmd
}

func (cmd *Command) Exec(ctx context.Context) error {
	defer close(cmd.done)

	execCommand := exec.CommandContext(ctx, cmd.command, cmd.args...)
	for _, f := range cmd.additional {
		if err := f(execCommand); err != nil {
			return err
		}
	}

	cmd.logger.Info("Exec", "command", append([]string{cmd.command}, cmd.args...), "workdir", execCommand.Dir)

	if err := execCommand.Run(); err != nil {
		return fmt.Errorf("run command %p: %w", execCommand, err)
	}

	return nil
}

func (cmd *Command) CheckOutput(ctx context.Context) ([]byte, error) {
	buf := &bytes.Buffer{}
	cmd.additional = append(cmd.additional, func(c *exec.Cmd) error {
		c.Stdout = buf
		return nil
	})
	if err := cmd.Exec(ctx); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type DockerRun struct {
	setup          *Setup
	Image          string
	ContainerName  string
	EntrypointPath optional.Optional[string]
	DockerOptions  []string
	Networks       []string
	Ports          []string
	Envs           []string
	Mounts         []string
	Args           []string
}

func (setup *Setup) DockerRun(image string, containerName string) *DockerRun {
	return &DockerRun{
		setup:          setup,
		Image:          image,
		ContainerName:  containerName,
		EntrypointPath: optional.None[string](),
		DockerOptions:  nil,
		Networks:       nil,
		Ports:          nil,
		Envs:           nil,
		Mounts:         nil,
		Args:           nil,
	}
}

func (r *DockerRun) Entrypoint(entrypointPath string) *DockerRun {
	r.EntrypointPath = optional.Some("--entrypoint=" + entrypointPath)
	return r
}

func (r *DockerRun) AddNetwork(networks ...string) *DockerRun {
	r.Networks = append(r.Networks, networks...)
	return r
}

func (r *DockerRun) DockerOption(key string, value string) *DockerRun {
	r.DockerOptions = append(r.DockerOptions, fmt.Sprintf("--%s=%s", key, value))
	return r
}

func (r *DockerRun) Option(key string, value string) *DockerRun {
	r.Args = append(r.Args, fmt.Sprintf("--%s=%s", key, value))
	return r
}

func (r *DockerRun) Options(pairs iter.Iter[iter.Pair[string, string]]) *DockerRun {
	pairs(func(pair iter.Pair[string, string]) iter.Flow {
		//nolint:ineffassign // false positive, see gordonklaus/ineffassign#95
		r = r.Option(pair.Left, pair.Right)
		return iter.Continue
	})
	return r
}

func (r *DockerRun) OptionMount(key string, hostPath string) *DockerRun {
	containerPath := fmt.Sprintf("/mnt/%d/%s", len(r.Mounts), path.Base(hostPath))
	r.Mounts = append(r.Mounts, fmt.Sprintf("%s:%s", hostPath, containerPath))
	r.Args = append(r.Args, fmt.Sprintf("--%s=%s", key, containerPath))
	return r
}

func (r *DockerRun) OptionMounts(pairs iter.Iter[iter.Pair[string, string]]) *DockerRun {
	pairs(func(pair iter.Pair[string, string]) iter.Flow {
		//nolint:ineffassign // false positive, see gordonklaus/ineffassign#95
		r = r.OptionMount(pair.Left, pair.Right)
		return iter.Continue
	})
	return r
}

func (r *DockerRun) OptionMountMap(key string, values []iter.Pair[string, string]) *DockerRun {
	dict := []string(nil)
	for _, value := range values {
		containerPath := fmt.Sprintf("/mnt/%d/%s", len(r.Mounts), path.Base(value.Right))
		r.Mounts = append(r.Mounts, fmt.Sprintf("%s:%s", value.Right, containerPath))
		dict = append(dict, fmt.Sprintf("%s=%s", value.Left, containerPath))
	}

	return r.Option(key, strings.Join(dict, ","))
}

func (r *DockerRun) PortForward(hostPort uint16, containerPort uint16) *DockerRun {
	r.Ports = append(r.Ports, fmt.Sprintf("%d:%d", hostPort, containerPort))
	return r
}

func (r *DockerRun) Env(key string, value string) *DockerRun {
	r.Envs = append(r.Envs, fmt.Sprintf("%s=%s", key, value))
	return r
}

func (r *DockerRun) Run(ctx context.Context) error {
	err := r.setup.Command(
		"docker", "run", "-d",
		"--restart=unless-stopped",
		fmt.Sprintf("--name=%s-%s", r.setup.Run.Namespace, r.ContainerName),
	).
		MoreArgs(r.EntrypointPath.AsSlice()...).
		MoreArgs(util.MapSlice(r.Networks, func(v string) string { return "--network=" + v })...).
		MoreArgs(util.MapSlice(r.Ports, func(v string) string { return "--expose=" + v })...).
		MoreArgs(util.MapSlice(r.Envs, func(v string) string { return "--env=" + v })...).
		MoreArgs(util.MapSlice(r.Mounts, func(v string) string { return "--volume=" + v })...).
		MoreArgs(r.DockerOptions...).
		MoreArgs(r.Image).
		MoreArgs(r.Args...).Exec(ctx)
	if err != nil {
		return fmt.Errorf("start container %q: %w", r.ContainerName, err)
	}
	return nil
}

func allocatePort() (uint16, error) {
	for {
		// Use ports in the range 40000..60000
		port := 40000 + uint16(rand.IntN(20000))

		// Create lock file to prevent other tests from acquiring this port
		file, err := os.OpenFile(fmt.Sprintf("/tmp/podseidon-e2e-lock-port-%d", port), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("create lock file /tmp/podseidon-e2e-lock-port-%d: %w", port, err)
		}

		if err := file.Close(); err != nil {
			return 0, fmt.Errorf("close lock file: %w", err)
		}

		// Test if this port is actually usable
		ln, err := net.Listen("tcp", net.JoinHostPort("", fmt.Sprint(port)))
		if errors.Is(err, syscall.EADDRINUSE) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("probe tcp port %d usability: %w", port, err)
		}

		if err := ln.Close(); err != nil {
			return 0, fmt.Errorf("close TCP probe listener: %w", err)
		}

		return port, nil
	}
}

func editJson[T any](data *[]byte, editor func(*T)) error {
	var value T
	err := json.Unmarshal(*data, &value)
	if err != nil {
		return fmt.Errorf("unmarshal JSON buffer as %v: %w", util.Type[T](), err)
	}

	editor(&value)

	buf := bytes.NewBuffer((*data)[:0])
	if err := json.NewEncoder(buf).Encode(value); err != nil {
		return fmt.Errorf("marshal %v into JSON: %w", util.Type[T](), err)
	}

	*data = buf.Bytes()
	return nil
}

func bufferedReadLog(logger logr.Logger, stopCh <-chan util.Empty) io.Writer {
	writer := make(chanWriter, 1)

	go func() {
		accum := ""

		for {
			select {
			case <-stopCh:
				if len(accum) > 0 {
					logger.Info(accum)
				}
				return
			case buf := <-writer:
				delim := strings.IndexByte(buf, '\n')
				if delim != -1 {
					line := strings.Builder{}
					_, _ = line.WriteString(accum)
					_, _ = line.WriteString(buf[:delim])
					logger.Info(line.String())

					accum = buf[delim+1:]
				} else {
					accum += buf
				}
			}
		}
	}()

	return writer
}

type chanWriter chan string

func (w chanWriter) Write(buf []byte) (int, error) {
	w <- string(buf)
	return len(buf), nil
}

func (setup *Setup) PollContainerAddressInNetwork(ctx context.Context, containerName string, networkName string) (string, error) {
	setup.Run.Logger.Info("Waiting for container address", "containerName", containerName, "networkName", networkName)

	var result string
	if err := wait.PollUntilContextTimeout(ctx, time.Second, time.Minute, true, func(ctx context.Context) (done bool, err error) {
		addr, err := setup.GetContainerAddressInNetwork(ctx, "podseidon-webhook", optional.Some(networkName))
		if err != nil || addr == "" {
			return false, err
		}

		result = addr
		return true, nil
	}); err != nil {
		return "", fmt.Errorf("waiting for %s to acquire address: %w", containerName, err)
	}

	return result, nil
}

func (setup *Setup) GetContainerAddress(ctx context.Context, containerName string) (string, error) {
	return setup.GetContainerAddressInNetwork(ctx, containerName, optional.None[string]())
}

var ErrNoContainerNetworks = errors.New("no container networks found")

func (setup *Setup) GetContainerAddressInNetwork(
	ctx context.Context,
	containerName string,
	requireNetworkName optional.Optional[string],
) (string, error) {
	type (
		Network struct {
			IPAddress string
		}
		NetworkSettings struct {
			Networks map[string]Network
		}
		Container struct {
			NetworkSettings NetworkSettings
		}
	)

	containerJson, err := setup.Command("docker", "inspect", fmt.Sprintf("%s-%s", setup.Run.Namespace, containerName)).CheckOutput(ctx)
	if err != nil {
		return "", err
	}

	var containers []Container
	if err := json.Unmarshal(containerJson, &containers); err != nil {
		return "", fmt.Errorf("decode docker inspect result for container %s: %w", containerName, err)
	}

	for _, container := range containers {
		for networkName, network := range container.NetworkSettings.Networks {
			if requireNetworkName.IsNoneOr(func(s string) bool { return s == networkName }) && network.IPAddress != "" {
				return network.IPAddress, nil
			}
		}
	}

	return "", fmt.Errorf("%w for container %s", ErrNoContainerNetworks, containerName)
}

func (setup *Setup) httpRequest(ctx context.Context, verb string, addr *url.URL, body []byte) ([]byte, error) {
	setup.Run.Logger.Info("http request", "addr", addr, "verb", verb, "body", string(body))

	req, err := http.NewRequestWithContext(ctx, verb, addr.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request error: %w", err)
	}

	//nolint:errcheck
	defer resp.Body.Close()

	output, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read http response: %w", err)
	}

	return output, nil
}

func httpJsonRequest[R any](ctx context.Context, setup *Setup, verb string, addr *url.URL, body any) (R, error) {
	var bodyJson []byte
	if body != nil {
		bodyJsonBytes, err := json.Marshal(body)
		if err != nil {
			return util.Zero[R](), fmt.Errorf("serialize json request body: %w", err)
		}
		bodyJson = bodyJsonBytes
	}

	resp, err := setup.httpRequest(ctx, verb, addr, bodyJson)
	if err != nil {
		return util.Zero[R](), err
	}

	var output R
	if err := json.Unmarshal(resp, &output); err != nil {
		return util.Zero[R](), fmt.Errorf("deserialize json response as %s: %w", util.Type[R]().String(), err)
	}

	return output, nil
}

type jaegerStructuredResponse[T any] struct {
	Data   T     `json:"data"`
	Total  int   `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
	Errors []any `json:"errors"`
}
