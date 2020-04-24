package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const (
	pgImage = "postgres:9.4-alpine"
)

var (
	cli            *client.Client
	defaultTimeout = 30 * time.Second
)

func main() {
	ctx := context.Background()
	createClient()
	imageName, err := reference.ParseNormalizedNamed(pgImage)
	if err != nil {
		fmt.Println("Unable to normalize name")
		panic(err)
	}
	fullName := imageName.String()

	pullContainer(ctx, fullName)
	id, _ := createNewContainer(ctx, fullName)
	removeContainer(ctx, id)

}

func createClient() {
	var err error
	cli, err = client.NewEnvClient()
	if err != nil {
		fmt.Println("Unable to create docker client")
		panic(err)
	}
}

func pullContainer(ctx context.Context, image string) {
	// TODO: cli.ImageList() to only download if not available
	out, err := cli.ImagePull(ctx, image, types.ImagePullOptions{})
	if err != nil {
		panic(err)
	}
	defer out.Close()

	io.Copy(os.Stdout, out)
}

func createNewContainer(ctx context.Context, image string) (string, error) {
	hostBinding := nat.PortBinding{
		HostIP:   "0.0.0.0",
		HostPort: "5432",
	}
	containerPort, err := nat.NewPort("tcp", "5432")
	if err != nil {
		panic("Unable to get the port")
	}

	portBinding := nat.PortMap{containerPort: []nat.PortBinding{hostBinding}}
	cont, err := cli.ContainerCreate(
		ctx,
		&container.Config{
			Image: image,
		},
		&container.HostConfig{
			PortBindings: portBinding,
		}, nil, "")

	cli.ContainerStart(ctx, cont.ID, types.ContainerStartOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Printf("Container %s is started\n", cont.ID)
	return cont.ID, nil
}

func removeContainer(ctx context.Context, id string) {
	err := cli.ContainerStop(ctx, id, &defaultTimeout)
	if err != nil {
		panic(err)
	}

	err = cli.ContainerRemove(ctx, id, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		// RemoveLinks=true causes "Error response from daemon: Conflict, cannot remove the default name of the container"
		RemoveLinks: false,
		Force:       false,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("Container %s is stopped\n", id)

}
