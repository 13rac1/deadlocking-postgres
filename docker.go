package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

func newDockerClient() (*dockerClient, error) {
	var err error
	cli, err = client.NewEnvClient()
	if err != nil {
		return nil, fmt.Errorf("unable to create docker client: %w", err)
	}
	return &dockerClient{*cli}, nil
}

type dockerClient struct {
	client.Client
}

func (d dockerClient) runContainer(ctx context.Context, image string, ports map[string]string, env []string) (*container.ContainerCreateCreatedBody, error) {
	// TODO: Use ports and env vars
	imageName, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return nil, fmt.Errorf("unable to normalize image name: %w", err)
	}
	fullName := imageName.String()

	// TODO: cli.ImageList() to only download if not available
	out, err := d.ImagePull(ctx, fullName, types.ImagePullOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to pull image: %w", err)
	}
	defer out.Close()

	io.Copy(os.Stdout, out)

	container, err := d.createNewContainer(ctx, fullName)
	if err != nil {
		return nil, fmt.Errorf("unable create container: %w", err)
	}
	err = d.ContainerStart(ctx, container.ID, types.ContainerStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to start the container: %w", err)
	}
	fmt.Printf("container %s is started\n", container.ID)
	return container, nil
}

func (d dockerClient) createNewContainer(ctx context.Context, image string) (*container.ContainerCreateCreatedBody, error) {
	hostBinding := nat.PortBinding{
		HostIP:   "0.0.0.0",
		HostPort: "5432",
	}
	containerPort, err := nat.NewPort("tcp", "5432")
	if err != nil {
		return nil, fmt.Errorf("unable to get the port: %w", err)
	}

	portBinding := nat.PortMap{containerPort: []nat.PortBinding{hostBinding}}
	cont, err := cli.ContainerCreate(
		ctx,
		&container.Config{
			Image: image,
			Env:   []string{"POSTGRES_PASSWORD=postgres"},
		},
		&container.HostConfig{
			PortBindings: portBinding,
		}, nil, "")

	return &cont, nil
}

func (d dockerClient) removeContainer(ctx context.Context, id string) error {
	fmt.Printf("container %s is stopping\n", id)
	err := cli.ContainerStop(ctx, id, &defaultTimeout)
	if err != nil {
		return fmt.Errorf("failed stopping container: %w", err)
	}
	fmt.Printf("container %s is stopped\n", id)

	err = cli.ContainerRemove(ctx, id, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		// RemoveLinks=true causes "Error response from daemon: Conflict, cannot
		// remove the default name of the container"
		RemoveLinks: false,
		Force:       false,
	})
	if err != nil {
		return fmt.Errorf("failed removing container: %w", err)
	}
	fmt.Printf("container %s is removed\n", id)
	return nil
}

func (d dockerClient) printLogs(ctx context.Context, id string) {
	out, err := cli.ContainerLogs(ctx, id, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
	})
	if err != nil {
		panic(err)
	}
	io.Copy(os.Stdout, out)
	defer out.Close()
}
