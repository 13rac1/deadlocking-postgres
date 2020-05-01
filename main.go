package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/docker/docker/client"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

const (
	pgImage         = "postgres:9.4-alpine"
	pgContainerName = "postgres"
	pgContainerPort = "5432"
	pgHostPort      = "5432"
	pgPassword      = "postgres"
)

var (
	cli            *client.Client
	defaultTimeout = 10 * time.Second
)

func main() {
	ctx := context.Background()
	docker, err := newDockerClient()
	if err != nil {
		panic(err)
	}

	serverHost := "127.0.0.1"
	serverPort := "5432"
	addr := fmt.Sprintf("%s:%s", serverHost, serverPort)
	ports := map[string]string{"5432": "5432"}
	env := []string{"POSTGRES_PASSWORD=postgres"}

	pgContainer, err := docker.runContainer(ctx, pgImage, ports, env)
	if err != nil {
		fmt.Println("error running container")
		panic(err)
	}

	defer docker.removeContainer(ctx, pgContainer.ID)
	go docker.printLogs(ctx, pgContainer.ID)

	err = waitForPostgresReady(ctx, addr)
	if err != nil {
		fmt.Println("failed waiting on Postgres")
		panic(err)
	}
}

func waitForPostgresReady(ctx context.Context, addr string) error {
	err := waitForPort(addr)
	if err != nil {
		return err
	}

	fmt.Println("connecting DB")
	// sqlx.Connect calls Ping() and will fail when the DB is not ready, so
	// manually ping the DB until ready.
	db, err := sqlx.Open("postgres", "postgres://postgres:postgres@localhost/postgres?sslmode=disable")
	if err != nil {
		return err
	}

	err = pingDB(ctx, db)
	if err != nil {
		return err
	}
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
