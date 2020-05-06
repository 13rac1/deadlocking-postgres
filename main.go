package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"syscall"
	"time"

	"github.com/docker/docker/client"
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

type pgStatActivity struct {
	Pid             int
	UserName        string `db:"usename"`
	ApplicationName string `db:"application_name"`
	ClientAddress   string `db:"client_addr"`
	Waiting         bool
	State           string
	Query           string
}

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

	db, err := waitForPostgresReady(ctx, addr)
	if err != nil {
		fmt.Println("failed waiting on Postgres")
		panic(err)
	}

	stopStatus := make(chan bool)
	go printConnectionStats(ctx, db, stopStatus)
	setupSchemaUsers(ctx, db)

	test1DB, err := sqlx.Connect("postgres", "postgres://test:test@localhost/postgres?sslmode=disable")
	if err != nil {
		panic(err)
	}

	tx0, err := db.Begin()
	if err != nil {
		panic(err)
	}

	tx1, err := test1DB.Begin()
	if err != nil {
		panic(err)
	}

	_, err = tx1.ExecContext(ctx,
		`INSERT INTO users(first_name, last_name, email)
		VALUES ($1, $2, $3) RETURNING "id";`,
		"test1", "test1", "test1@example.com")
	if err != nil {
		panic("tx1-1: " + err.Error())
	}

	go func() {
		_, err = tx0.ExecContext(ctx,
			`INSERT INTO users(first_name, last_name, email)
			VALUES ($1, $2, $3) RETURNING "id";`,
			"test2", "test2", "test2@example.com")
		if err != nil {
			log.Print("tx0-1: " + err.Error())
			return
		}
		// Conflicts with INSERT of test1@example.com
		_, err = tx0.ExecContext(ctx,
			`ALTER TABLE users ADD COLUMN counter TEXT;`)
		if err != nil {
			log.Print("tx0-2: " + err.Error())
		}
	}()
	time.Sleep(100 * time.Millisecond)
	// Conflicts with INSERT of test2@example.com
	_, err = tx1.ExecContext(ctx,
		`INSERT INTO users(first_name, last_name, email)
		VALUES ($1, $2, $3) RETURNING "id";`,
		"test3", "test3", "test2@example.com")
	if err != nil {
		panic("tx1-2: " + err.Error())
	}

	err = tx1.Commit()
	if err != nil {
		panic(err)
	}

	err = test1DB.Close()
	if err != nil {
		panic(err)
	}

	// Disabled, because this a failed transaction.
	// err = tx0.Commit()
	// if err != nil {
	// 	panic(err)
	// }

	err = db.Close()
	if err != nil {
		panic(err)
	}

	// Stop connection status display
	stopStatus <- true
}

func printConnectionStats(ctx context.Context, db *sqlx.DB, stop chan bool) {
	for true {
		select {
		case <-stop:
			fmt.Println("Stopping status loop")
			return
		default:
			var statActivity []pgStatActivity
			err := db.SelectContext(ctx, &statActivity, "select pid, usename, application_name, client_addr, waiting, state, query from pg_stat_activity;")
			if err != nil {
				panic(err)
			}
			fmt.Printf("\n%#v count:%d\n", statActivity, len(statActivity))
			time.Sleep(1 * time.Second)
		}
	}
}

func setupSchemaUsers(ctx context.Context, db *sqlx.DB) {
	_, err := db.Exec(`
	CREATE TABLE users (
		id SERIAL PRIMARY KEY,
		first_name TEXT,
		last_name TEXT,
		email TEXT,
		UNIQUE (email)
	)`)
	if err != nil {
		panic(err)
	}

	_, err = db.Exec(`CREATE USER test WITH ENCRYPTED PASSWORD 'test';`)
	if err != nil {
		panic(err)
	}

	_, err = db.Exec(`GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO test;`)
	if err != nil {
		panic(err)
	}
	_, err = db.Exec(`GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO test;`)
	if err != nil {
		panic(err)
	}
}

func waitForPostgresReady(ctx context.Context, addr string) (*sqlx.DB, error) {
	err := waitForPort(addr)
	if err != nil {
		return nil, err
	}

	fmt.Println("connecting DB")
	// sqlx.Connect calls Ping() and will fail when the DB is not ready, so
	// manually ping the DB until ready.
	db, err := sqlx.Open("postgres", "postgres://postgres:postgres@localhost/postgres?sslmode=disable")
	if err != nil {
		return nil, err
	}

	err = pingDB(ctx, db)
	if err != nil {
		return nil, err
	}
	return db, nil
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
		time.Sleep(500 * time.Millisecond)
		return db.PingContext(ctx)
	}

	for err != nil {
		//fmt.Printf("%#v\n", db.Stats())

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
