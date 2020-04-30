package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

const (
	pgImage = "postgres:9.4-alpine"
)

var (
	cli            *client.Client
	defaultTimeout = 10 * time.Second
)

func main() {
	ctx := context.Background()
	err := createClient()
	if err != nil {
		panic(err)
	}

	imageName, err := reference.ParseNormalizedNamed(pgImage)
	if err != nil {
		fmt.Println("Unable to normalize name")
		panic(err)
	}
	fullName := imageName.String()

	err = pullContainer(ctx, fullName)
	if err != nil {
		panic(err)
	}

	container, err := createNewContainer(ctx, fullName)
	if err != nil {
		panic(err)
	}

	defer removeContainer(ctx, container.ID)

	// Print logs from the container
	go func() {
		out, err := cli.ContainerLogs(ctx, container.ID, types.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
			Timestamps: true,
		})
		if err != nil {
			panic(err)
		}
		//io.Copy(os.Stdout, out)
		defer out.Close()
	}()

	serverHost := "127.0.0.1"
	serverPort := "5432"
	err = waitForPort(fmt.Sprintf("%s:%s", serverHost, serverPort))
	if err != nil {
		panic(err)
	}

	fmt.Println("connecting DB")
	// sqlx.Connect calls Ping() and will fail when the DB is not ready, so
	// manually ping the DB until ready.
	db, err := sqlx.Open("postgres", "postgres://postgres:postgres@localhost/postgres?sslmode=disable")
	if err != nil {
		panic(err)
	}

	err = pingDB(ctx, db)
	if err != nil {
		panic(err)
	}

}

func createClient() error {
	var err error
	cli, err = client.NewEnvClient()
	if err != nil {
		return fmt.Errorf("unable to create docker client: %w", err)
	}
	return nil
}

func pullContainer(ctx context.Context, image string) error {
	// TODO: cli.ImageList() to only download if not available
	out, err := cli.ImagePull(ctx, image, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("unable to pull image: %w", err)
	}
	defer out.Close()

	io.Copy(os.Stdout, out)
	return nil
}

func createNewContainer(ctx context.Context, image string) (*container.ContainerCreateCreatedBody, error) {
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

	err = cli.ContainerStart(ctx, cont.ID, types.ContainerStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to start the container: %w", err)
	}
	fmt.Printf("container %s is started\n", cont.ID)
	return &cont, nil
}

func removeContainer(ctx context.Context, id string) error {
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

func waitForPort(addr string) error {
	count := 0
	fmt.Println("checking for open port")

	conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
	for err != nil {
		count++
		if count > 10 {
			return fmt.Errorf("cannot connect to server: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
		fmt.Println("retrying Port")
		conn, err = net.DialTimeout("tcp", addr, 1*time.Second)
	}
	conn.Close()
	fmt.Println("port connected")
	return nil
}

func pingDB(ctx context.Context, db *sqlx.DB) error {
	fmt.Println("pinging DB")
	err := db.PingContext(ctx)

	retry := func(err error) error {
		fmt.Printf("%#v: %s\n", err, err)
		time.Sleep(250 * time.Millisecond)
		return db.PingContext(ctx)
	}

	for err != nil {
		fmt.Printf("%#v\n", db.Stats())

		// Connections to a PG database fail with "connection reset by peer" or
		// "EOF" until the database has started to respond, then connections will
		// fail with "the database system is starting up" until the database is
		// ready.
		var errNo syscall.Errno
		if errors.As(err, &errNo) {
			if errNo == 0x68 { //"connection reset by peer" on Linux
				err = retry(errNo)
				continue
			}
		}
		if err == io.EOF {
			err = retry(errNo)
			continue
		}
		var errPq *pq.Error
		if errors.As(err, &errPq) {
			// "the database system is starting up"
			if errPq.Code == "57P03" {
				err = retry(errNo)
				continue
			}
		}
		fmt.Printf("%#v: %s\n", err, err)
		return err
	}
	fmt.Printf("%#v\n", db.Stats())
	fmt.Println("DB pinged")
	return nil
}
