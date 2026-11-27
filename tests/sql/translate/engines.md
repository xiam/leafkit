# Run the optional engine comparison

Run `TestEngineParity` with two disposable containers that belong to this
run. It replaces their `leafkit_translate` table using
[`testdata/setup.sql`](testdata/setup.sql). Do not point it at shared databases.
Docker, a Linux x86-64 host and Go 1.23 or later are required. SQL Server's
Developer edition is for development and testing. The setup below uses no
host ports and has no network access after image download.

Pull the images, then create the containers. Set a test password of your own.
The image digests make this procedure reproducible; tags are shown as context
(PostgreSQL 18 and SQL Server 2022).

```sh
export LEAFKIT_PG_CONTAINER=leafkit-translate-pg
export LEAFKIT_TSQL_CONTAINER=leafkit-translate-ms
export LEAFKIT_TSQL_PASSWORD='Choose_A_Test_Password_22'
docker pull postgres@sha256:64c8b0d31e4ab6ea665e8426ea406a94bf52de3c05b3a4a8bf713704eefa9328
docker pull mcr.microsoft.com/mssql/server@sha256:4525e3efac9f3e241cb003bb2ff2a82818b502f37e12049d788b1a9fa7e13b7f
docker run -d --name "$LEAFKIT_PG_CONTAINER" --network none \
  --memory 1g --memory-swap 1g -e POSTGRES_HOST_AUTH_METHOD=trust \
  postgres@sha256:64c8b0d31e4ab6ea665e8426ea406a94bf52de3c05b3a4a8bf713704eefa9328
docker run -d --name "$LEAFKIT_TSQL_CONTAINER" --network none \
  --memory 3g --memory-swap 3g -e ACCEPT_EULA=Y -e MSSQL_PID=Developer \
  -e MSSQL_SA_PASSWORD="$LEAFKIT_TSQL_PASSWORD" \
  mcr.microsoft.com/mssql/server@sha256:4525e3efac9f3e241cb003bb2ff2a82818b502f37e12049d788b1a9fa7e13b7f
```

Wait for both engines to accept a connection. These commands must succeed:

```sh
docker exec "$LEAFKIT_PG_CONTAINER" pg_isready -U postgres
SQLCMDPASSWORD="$LEAFKIT_TSQL_PASSWORD" docker exec -e SQLCMDPASSWORD \
  "$LEAFKIT_TSQL_CONTAINER" /opt/mssql-tools18/bin/sqlcmd \
  -S tcp:127.0.0.1,1433 -U sa -C -b -Q 'SELECT 1'
```

Run the opt-in check from the module root. On a systemd runner use the memory
scope below. The containers have their own memory caps; the scope caps Go.

```sh
LEAFKIT_ENGINE_PARITY=1 systemd-run --user --scope \
  -p MemoryMax=8G -p MemorySwapMax=0 -q -- \
  go test -v -count=1 ./tests/sql/translate -run '^TestEngineParity$'
```

`TestEngineParity` logs engine versions and compares source and translated
ordered rows with the [TOP fixtures](testdata/top.json). It also runs the
[tie fixture](testdata/ties.json): SQL Server returns four scores, while the
explicitly lossy LIMIT returns three. The translator rejects that source
before any target is produced. The [alias fixture](testdata/alias.json) also executes the review's bare-key
counterexample and its historical unchecked target, logging actual rows.
Their cutoff has ties, so the test checks permissible rows without requiring
an accidental tie order or a difference on every run. Translation must reject
that source with an `ordering-key` range diagnostic. Qualified-key and proven
key-alias controls remain accepted and must match ordered row oracles.
The test reports a failure if either configured
engine is unavailable. It skips when `LEAFKIT_ENGINE_PARITY` is unset, so report
an actual opt-in result separately from ordinary parser tests.

Remove only the two containers created for this run, including their anonymous
volumes. Leave shared images and other containers alone.

```sh
docker rm -fv "$LEAFKIT_PG_CONTAINER" "$LEAFKIT_TSQL_CONTAINER"
unset LEAFKIT_PG_CONTAINER LEAFKIT_TSQL_CONTAINER LEAFKIT_TSQL_PASSWORD
```
