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
	createClient()
	imageName, err := reference.ParseNormalizedNamed(pgImage)
	if err != nil {
		fmt.Println("Unable to normalize name")
		panic(err)
	}
	fullName := imageName.String()

	pullContainer(ctx, fullName)
	container, _ := createNewContainer(ctx, fullName)
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
	waitForPort(fmt.Sprintf("%s:%s", serverHost, serverPort))

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

func createNewContainer(ctx context.Context, image string) (container.ContainerCreateCreatedBody, error) {
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
			Env:   []string{"POSTGRES_PASSWORD=postgres"},
		},
		&container.HostConfig{
			PortBindings: portBinding,
		}, nil, "")

	err = cli.ContainerStart(ctx, cont.ID, types.ContainerStartOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Printf("Container %s is started\n", cont.ID)
	return cont, nil
}

func removeContainer(ctx context.Context, id string) {
	fmt.Printf("Container %s is shutting down\n", id)
	err := cli.ContainerStop(ctx, id, &defaultTimeout)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Container %s is stopped\n", id)

	err = cli.ContainerRemove(ctx, id, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		// RemoveLinks=true causes "Error response from daemon: Conflict, cannot
		// remove the default name of the container"
		RemoveLinks: false,
		Force:       false,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("Container %s is removed\n", id)

}

func waitForPort(addr string) {
	count := 0
	fmt.Println("Checking for open port")

	conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
	for err != nil {
		count++
		if count > 10 {
			fmt.Printf("Can't connect to server: %s\n", err)
			return
		}
		time.Sleep(100 * time.Millisecond)
		fmt.Println("Retrying Port")
		conn, err = net.DialTimeout("tcp", addr, 1*time.Second)
	}
	conn.Close()
	fmt.Println("Port connected")
}

func pingDB(ctx context.Context, db *sqlx.DB) error {
	fmt.Println("pinging DB")
	err := db.PingContext(ctx)

	for err != nil {
		fmt.Printf("%#v\n", db.Stats())

		// Connections to a PG database fail with "connection reset by peer" or
		// "EOF" until the database has started to respond, then connections will
		// fail with "the database system is starting up" until the database is
		// ready.
		var errNo syscall.Errno
		if errors.As(err, &errNo) {
			fmt.Printf("%#v: %s\n", errNo, errNo)
			if errNo == 0x68 { //"connection reset by peer" on Linux
				time.Sleep(250 * time.Millisecond)
				err = db.PingContext(ctx)
				continue
			}
		}
		if err == io.EOF {
			fmt.Printf("%#v: %s\n", err, err)
			time.Sleep(250 * time.Millisecond)
			err = db.PingContext(ctx)
			continue
		}
		var errPq *pq.Error
		if errors.As(err, &errPq) {
			// "the database system is starting up"
			if errPq.Code == "57P03" {
				fmt.Printf("%#v: %s\n", errPq, errPq)
				time.Sleep(250 * time.Millisecond)
				err = db.PingContext(ctx)
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
