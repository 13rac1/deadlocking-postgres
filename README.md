# Reproducing Postgres Deadlocks

What is a Postgres Deadlock? How do they happen?

This Go application uses Docker to start a Postgres instance and create a Deadlock.

## Documentation

Relevant information to understand what is being reproduced and why.

* http://shiroyasha.io/deadlocks-in-postgresql.html
* https://rcoh.svbtle.com/postgres-unique-constraints-can-cause-deadlock
* https://www.citusdata.com/blog/2018/02/22/seven-tips-for-dealing-with-postgres-locks/
* https://www.compose.com/articles/common-misconceptions-about-locking-in-postgresql/
* https://stackoverflow.com/questions/31854683/postgres-deadlock-detector-not-always-working
* https://www.postgresql.org/docs/9.3/runtime-config-client.html
* https://stackoverflow.com/questions/14530360/what-is-row-exclusive-in-postgresql-exactly
* https://www.percona.com/blog/2018/10/24/postgresql-locking-part-2-heavyweight-locks/

## Relevant log output of the Deadlock

```bash
[]main.pgStatActivity{
  main.pgStatActivity{Pid:63, UserName:"postgres", ApplicationName:"", ClientAddress:"172.17.0.1", Waiting:true, State:"active", Query:"ALTER TABLE users ADD COLUMN counter TEXT;"},
  main.pgStatActivity{Pid:64, UserName:"postgres", ApplicationName:"", ClientAddress:"172.17.0.1", Waiting:false, State:"active", Query:"select pid, usename, application_name, client_addr, waiting, state, query from pg_stat_activity;"},
  main.pgStatActivity{Pid:65, UserName:"test", ApplicationName:"", ClientAddress:"172.17.0.1", Waiting:true, State:"active", Query:"INSERT INTO users(\n\t\tfirst_name, last_name, email)\n\tVALUES ($1, $2, $3) RETURNING \"id\";"}
} count:3
2020/05/05 23:12:32 tx0-2: pq: deadlock detected
2020-05-06T06:12:32.532733678Z ERROR:  deadlock detected
2020-05-06T06:12:32.532887993Z DETAIL:  Process 63 waits for AccessExclusiveLock on relation 16386 of database 12138; blocked by process 65.
2020-05-06T06:12:32.532931340Z         Process 65 waits for ShareLock on transaction 684; blocked by process 63.
2020-05-06T06:12:32.532956732Z         Process 63: ALTER TABLE users ADD COLUMN counter TEXT;
2020-05-06T06:12:32.532976401Z         Process 65: INSERT INTO users(
2020-05-06T06:12:32.532994772Z                         first_name, last_name, email)
2020-05-06T06:12:32.533069573Z                 VALUES ($1, $2, $3) RETURNING "id";
2020-05-06T06:12:32.533094047Z HINT:  See server log for query details.
2020-05-06T06:12:32.533113567Z STATEMENT:  ALTER TABLE users ADD COLUMN counter TEXT;
```
