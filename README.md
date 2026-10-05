# Relay

Relay is a fee-paying transaction relay for BSV. Clients send a transaction, the relay adds fee
inputs from its own wallet, stores it, broadcasts it through ARC and keeps rebroadcasting until it is
mined.

## Contents

- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Getting started](#getting-started)
- [API](#api)
- [Configuration](#configuration)
- [Keys and addresses](#keys-and-addresses)
- [Networks](#networks)
- [Observability](#observability)
- [Running multiple instances](#running-multiple-instances)
- [Production notes](#production-notes)
- [Development](#development)

## How it works

**Fee utxos.** The relay keeps a pool of small pre-split utxos in RabbitMQ, one queue per
denomination (50, 100, 200 … 12800 sats). A funder tops the queues up to `fund_amount` from the
funding address and refills a queue every time one of its utxos is used.

**Submitting a transaction.** For `/fund-and-broadcast` the relay

1. checks every input with the script engine,
2. works out what is missing: `outputs + fee - client inputs`, rejecting anything above
   `max_sponsor_sats`,
3. takes fee utxos covering that amount and reserves them in Redis so no other request can use them,
4. signs its inputs with `SIGHASH_ALL | ANYONECANPAY`, checks the whole transaction again and stores
   it in Postgres together with the outputs it spends,
5. broadcasts it once and returns the stored state.

A transaction with no inputs is fully paid by the relay (outputs and fee). A transaction that already
pays for itself gets no fee input. Client inputs must be sent in extended format so they can be
validated, and must be signed with `ANYONECANPAY` so the relay can add its own inputs.

**After submitting.** A syncer rebroadcasts every stored transaction each `sync.rebroadcast_interval`
until ARC reports it mined (`SYNCED`). It is marked `FAILED` after `sync.max_attempts` rejections or
once it is older than `sync.max_age`. ARC being unreachable never counts as an attempt. Failing a
transaction also fails every pending transaction chained off it.

**Safety.**

- Every spent output is recorded, so a second transaction spending the same output is rejected as a
  double spend, also across instances.
- Fee utxos are only acknowledged in RabbitMQ once the transaction using them is stored. A crash at
  any point returns them to the queue, and spent ones are dropped when they come back.
- Funding transactions are stored before they are broadcast and their utxos are published to
  RabbitMQ from Postgres with publisher confirms, so nothing is lost on a crash.
- Utxos left behind by failed transactions are checked on WhatsOnChain every `recovery_interval` and
  returned to the pool when they are still unspent.
- ARC calls go to TAAL first and to a fallback ARC when TAAL is unreachable. The fee rate is read
  from the ARC policy.

## Requirements

- Docker with Compose (or Podman with podman-compose)
- A TAAL ARC token
- Go 1.27+ only to build or test outside Docker

Compose runs everything the relay needs: PostgreSQL, RabbitMQ, Redis and Grafana (`otel-lgtm`).

## Getting started

1. Create the config:

   ```bash
   cp config-example.yaml config.yaml
   ```

2. In `config.yaml` set at least:

   - `app.network`: `MAIN` or `TEST`
   - `arc.token`: your TAAL ARC token
   - `auth.tokens[].token`: a random string of 32+ characters, for example
     `openssl rand -hex 32`

3. Start the stack:

   ```bash
   docker compose up --build
   ```

   The relay listens on `:8080`. Database migrations run on startup.

4. Fund the relay. Get the funding address and send coins to it:

   ```bash
   curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/funding-address
   ```

   Deposits are picked up within `funding_scan_interval` and used to fill the fee queues.

5. Send a transaction:

   ```bash
   curl -H "Authorization: Bearer $TOKEN" \
        -d '{"txHex":"<transaction hex>"}' \
        http://localhost:8080/fund-and-broadcast
   ```

## API

Every endpoint except `/health` needs authentication, see [`auth`](#auth).

### `POST /fund-and-broadcast`

Adds fee inputs covering whatever the transaction is missing, stores it and broadcasts it.

Request:

```json
{ "txHex": "<transaction hex, extended format when it has inputs>" }
```

Response `200`, the stored transaction:

```json
{
  "txid": "ab12…",
  "status": "BROADCASTED",
  "attempts": 1,
  "lastBroadcastAt": 1791193535,
  "createdAt": 1791193535
}
```

`status` is `PENDING` when the first broadcast could not reach ARC. The relay keeps retrying, the
request does not fail.

### `POST /broadcast`

Stores and broadcasts a transaction that already pays its own fee. Raw (non extended) hex is
accepted, inputs without a known source output are left for ARC to validate. Same request and
response as `/fund-and-broadcast`.

### `GET /tx?txid=<txid>`

Returns the stored state of a transaction, same shape as above. Once mined it includes `blockHash`
and `blockHeight`, once failed it includes `lastError`.

### `GET /funding-address`

```json
{ "address": "1…" }
```

### `GET /health`

Unauthenticated liveness check, returns `{"status":"ok"}`.

### Transaction states

| Status | Meaning |
|---|---|
| `PENDING` | Stored, not yet accepted by ARC |
| `BROADCASTED` | Accepted by ARC, waiting to be mined |
| `SYNCED` | Mined |
| `FAILED` | Rejected `sync.max_attempts` times, older than `sync.max_age`, or chained off a failed transaction |

### Errors

| Code | When |
|---|---|
| `400` | Invalid transaction: bad hex, failed script check, inputs don't cover outputs and fee, double spend, needs more than `max_sponsor_sats`, malformed txid |
| `401` | Missing or wrong credentials |
| `404` | Unknown txid |
| `405` | Wrong HTTP method |
| `500` | Internal error |
| `503` | No fee utxo available in time, the fee queues are empty |

## Configuration

`config.yaml` is read from the working directory. Durations use Go syntax (`10s`, `5m`, `24h`).

### `app`

| Key | Default | Description |
|---|---|---|
| `app.network` | required | `MAIN` or `TEST`, see [Networks](#networks) |
| `app.addr` | | Listen address, `:8080` in the example |

### `auth`

| Key | Default | Description |
|---|---|---|
| `auth.mode` | required | `token`, `basic` or `none` |
| `auth.tokens[]` | | `name` and `token` (32+ characters) per client, used with `token` |
| `auth.users[]` | | `username` and `password` (12+ characters), used with `basic` |

With `token` clients send `Authorization: Bearer <token>`. With `basic` they use HTTP basic auth.
`none` leaves every endpoint open and is only meant for local development. The relay refuses to
start on a missing mode, empty or duplicate credentials, or secrets below the minimum length.

### `arc`

| Key | Default | Description |
|---|---|---|
| `arc.token` | required | TAAL ARC token |
| `arc.fallback_url` | GorillaPool on `MAIN`, none on `TEST` | ARC used when TAAL is unreachable |
| `arc.fallback_token` | | Token for the fallback ARC, if it needs one |

### `woc`

| Key | Default | Description |
|---|---|---|
| `woc.token` | | WhatsOnChain API key |

### Infrastructure

| Key | Default | Description |
|---|---|---|
| `db.url` | required | PostgreSQL URL |
| `redis.url` | required | Redis URL |
| `rabbitmq.url` | | RabbitMQ URL |
| `rabbitmq.prefetch` | `50` | Unacked fee utxos per queue per instance, the most concurrent requests one queue can serve |

### Funding

| Key | Default | Description |
|---|---|---|
| `fund_amount` | `1` | Fee utxos kept in each queue |
| `max_sponsor_sats` | `20000` | Most sats the relay adds to one transaction |
| `funding_scan_interval` | `5m` | How often the funding address is checked for deposits |
| `recovery_interval` | `10m` | How often utxos of failed transactions are checked for recovery |
| `fee_policy_interval` | `30m` | How often the fee rate is read from the ARC policy |

### `sync`

| Key | Default | Description |
|---|---|---|
| `sync.max_attempts` | `5` | ARC rejections before a transaction is `FAILED` |
| `sync.max_age` | `24h` | Age after which an unmined transaction is `FAILED` |
| `sync.rebroadcast_interval` | `10m` | Wait for a transaction to be mined before rebroadcasting it |
| `sync.poll_interval` | `10s` | How often the syncer looks for due transactions |
| `sync.batch_size` | `50` | Transactions handled per poll |

### `telemetry` and `log`

| Key | Default | Description |
|---|---|---|
| `telemetry.enabled` | `false` | Export traces, metrics and logs over OTLP |
| `telemetry.otlp_endpoint` | | OTLP HTTP endpoint, `http://otel-lgtm:4318` in compose |
| `log.level` | `info` | `debug`, `info`, `warn` or `error` |

## Keys and addresses

Keys live in `.key/`, mounted into the container at `/app/.key`.

- On start the relay loads `.key/wif.txt`, otherwise derives a key from `.key/mnemonic.txt` (with
  `key.password` as the BIP39 passphrase, empty by default), otherwise generates a new key and writes
  `wif.txt`, `mnemonic.txt`, `address.txt` and `pubkey.txt`.
- A `wif.txt` that exists but cannot be parsed is treated as missing. Without a mnemonic a new key
  is generated and **`wif.txt` is overwritten**, so keep a backup of it.
- To use your own key, put its WIF in `.key/wif.txt`.
- The relay uses two addresses. The **funding address** (from `wif.txt`) receives deposits and
  funding change. The **fee address** holds the split fee utxos and is derived from the same key, so
  `wif.txt` is the only secret to back up.

## Networks

`app.network` selects `MAIN` or `TEST`: the ARC and WhatsOnChain endpoints, the address format and
where state is kept. State is separated per network:

- PostgreSQL: one schema per network, `relay_main` and `relay_test`
- RabbitMQ: queue names prefixed with the network, `MAIN.QUEUE_50`
- Redis: keys prefixed with the network, `relay:MAIN:`

Switching the flag works on the other network's state and leaves the current one untouched. Both
networks can share the same PostgreSQL, RabbitMQ and Redis.

## Observability

Logs are JSON on stdout. With `telemetry.enabled` traces, metrics and logs are also exported over
OTLP. Compose runs `grafana/otel-lgtm` (an OpenTelemetry collector with Prometheus, Tempo, Loki and
Grafana), Grafana is on http://localhost:3000. Log lines written inside a request or background job
carry `trace_id`, so Grafana can link a trace to its logs.

Traced: HTTP requests, taking fee utxos, storing, broadcasting, each syncer pass over a transaction,
funding rounds, recovery runs and every outgoing ARC and WhatsOnChain call.

| Metric | Description |
|---|---|
| `relay.funding.balance` | Unspent funding sats. Alert when low, requests fail with `503` once the queues drain |
| `relay.tx.count{status}` | Stored transactions by status |
| `relay.tx.settled{status,reason}` | Transactions reaching `SYNCED` or `FAILED` (`max_attempts`, `expired`, `parent_failed`) |
| `relay.tx.time_to_mined` | Seconds from storing a transaction to it being mined |
| `relay.tx.submitted{endpoint,result}` | Requests by result: `stored`, `invalid`, `out_of_fee`, `error` |
| `relay.arc.requests{provider,operation,result}` | ARC calls: `ok`, `rejected`, `unreachable` |
| `relay.fee_utxo.events{queue,event}` | Fee utxos taken, parked, dropped, requeued |
| `relay.funding.rounds{result}`, `relay.funding.outputs{queue}` | Funding transactions and the fee utxos they created |
| `relay.funding.owed{queue}`, `relay.queue_utxo.unpublished` | Refills owed and fee utxos waiting to be published |
| `relay.utxo.recovery{kind,result}` | Utxos checked for recovery |
| `relay.fee.rate` | Fee rate in use, sat/kB |
| `relay.auth.rejected`, `relay.rabbitmq.reconnects` | Rejected requests and broker reconnects |

Worth alerting on: low `relay.funding.balance`, `relay.tx.submitted{result="out_of_fee"}`,
`relay.arc.requests{result="unreachable"}` and a growing `relay.tx.count{status="FAILED"}`.

## Running multiple instances

Instances can share PostgreSQL, RabbitMQ and Redis. Funding runs under a Redis lock, the syncer
claims transactions with `FOR UPDATE SKIP LOCKED`, fee utxos are reserved in Redis and migrations run
under a PostgreSQL advisory lock.

- Create `.key/wif.txt` before starting more than one instance, otherwise each instance generates
  its own key.
- The compose file runs a single `app` with a fixed container name and port. Scaling needs those
  removed and a load balancer in front.
- Refills owed by an instance are kept in memory and lost if it crashes, leaving its queues slightly
  below `fund_amount`.

## Production notes

- Run the relay behind a TLS proxy (Caddy, nginx). Tokens and passwords are readable over plain
  HTTP.
- `.key/wif.txt` is a hot wallet in a plaintext file. Keep only the balance you need on it.
- `otel-lgtm` is meant for development. In production point `telemetry.otlp_endpoint` at a proper
  Grafana stack or Grafana Cloud, and change Grafana's default `admin` / `admin` login if it is
  reachable.
- There is no per-client rate limit yet. `max_sponsor_sats` caps each request but not the number of
  requests.

## Development

```bash
make build
go test ./...
```

Tests that need PostgreSQL are skipped unless `TEST_DATABASE_URL` points at a database the user can
create schemas in. Each test uses its own schema:

```bash
docker run -d --name relay-pg-test -e POSTGRES_USER=relay -e POSTGRES_PASSWORD=relay \
  -e POSTGRES_DB=relay -p 54329:5432 postgres:17-alpine
TEST_DATABASE_URL="postgres://relay:relay@localhost:54329/relay?sslmode=disable" go test -race ./...
```

Project layout:

| Path | Contents |
|---|---|
| `main.go` | Wiring and startup |
| `internal/auth` | Token and basic auth middleware |
| `internal/broadcaster` | ARC clients with fallback, WhatsOnChain client |
| `internal/config` | Config loading and network validation |
| `internal/db` | PostgreSQL client, embedded migrations, repository |
| `internal/handlers` | HTTP handlers and router |
| `internal/keymanager` | Funding and fee keys |
| `internal/rabbitmq` | Fee utxo queues, reconnecting consumers, confirmed publishing |
| `internal/reservation` | Redis reservations and locks |
| `internal/services` | Funding, fee utxos, syncer, recovery, validation, metrics |
| `internal/telemetry` | Logging, tracing and metrics setup |
