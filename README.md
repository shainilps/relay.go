# Relay

A lightweight transaction relay service for BSV development that funds fees and broadcasts
transactions.

## Goal

Reduce repetitive tasks like fee handling and broadcasting, so you can get started in under a
minute.

## Keys Info

- Keys live under the `.key` directory: `.key/wif.txt`
- Automatically generated files:
  - `.key/wif.txt`
  - `.key/mnemonic.txt`
  - `.key/address.txt`
  - `.key/pubkey.txt`

> To use an existing key, just place `wif.txt` in the `.key` directory. Only WIF is required.

---

## How to Start

Minimal setup (3 necessary + 1 optional):

1. Add your key `wif.txt` in the `.key` directory (optional; the server will generate one if not
   present).
2. Add `arc.token` in `config.yaml` according to the network (`mainnet` or `test`).
3. Rename the example config:

   ```bash
   mv config.example.yaml config.yaml
   ```

4. Start the services using Docker or Podman:

   ```bash
   docker-compose up --build
   # or
   podman-compose up --build
   ```

---

## Configuration

- **Port:** 8080 (default)
- **Database:** PostgreSQL (`db.url`), persisted via Docker volume
- **Fee rate:** `fee.sat_per_byte = 100` (as of Nov 15) — change only if policy changes
- **Taal ARC token:** required, set in `config.yaml`
- **Auth:** `auth.mode` is required: `token` (clients send `Authorization: Bearer <token>`, tokens of
  32+ characters, one named token per client), `basic` (username and password of 12+ characters) or
  `none` (local development only). Every endpoint except `GET /health` needs it. Put the relay behind
  a TLS proxy (Caddy, nginx) in production, tokens and passwords are readable over plain HTTP.

> For current fee rates:
>
> ```bash
> curl --location 'https://arc.taal.com/v1/policy' | jq
> ```

---

## Observability

Logs (zap, JSON on stdout), traces and metrics are exported over OTLP when `telemetry.enabled` is
true. `docker compose up` starts `grafana/otel-lgtm` (Prometheus, Tempo, Loki and Grafana in one
container), Grafana is on http://localhost:3000. Log lines written inside a request or background
job carry `trace_id`, so Grafana can jump between a trace and its logs.

Metrics:

| Metric | What it tells you |
|---|---|
| `relay.funding.balance` | Unspent funding sats. Alert when low, the relay returns 503 once the queues drain. |
| `relay.tx.count{status}` | Stored txs by status. Alert on a growing `FAILED` or a stuck `PENDING`. |
| `relay.tx.settled{status,reason}` | Txs reaching `SYNCED` or `FAILED` (`max_attempts`, `expired`, `parent_failed`). |
| `relay.tx.time_to_mined` | Seconds from storing a tx to it being mined. |
| `relay.tx.submitted{endpoint,result}` | Requests by result: `stored`, `invalid`, `out_of_fee`, `error`. Alert on `out_of_fee`. |
| `relay.arc.requests{provider,operation,result}` | Arc calls: `ok`, `rejected`, `unreachable`. Alert on `unreachable`. |
| `relay.fee_utxo.events{queue,event}` | Fee utxos taken, parked, dropped. |
| `relay.funding.rounds{result}` / `relay.funding.outputs{queue}` | Funding txs and the fee utxos they created. |
| `relay.funding.owed{queue}` / `relay.queue_utxo.unpublished` | Refills owed and fee utxos waiting to be published. |
| `relay.utxo.recovery{kind,result}` | Utxos recovered from failed txs. |
| `relay.fee.rate` | Fee rate in use (sat/kB). |
| `relay.auth.rejected` / `relay.rabbitmq.reconnects` | Rejected requests and broker reconnects. |

---

## Notes on Volumes

- `.key` directory is **mounted** to persist keys:

  ```yaml
  volumes:
    - ./key:/app/.key
  ```

- PostgreSQL data is persisted via volume:

  ```yaml
  volumes:
    - postgres_data:/var/lib/postgresql/data
  ```

- RabbitMQ data is persisted via volume:

  ```yaml
  volumes:
    - rabbitmq_data:/var/lib/rabbitmq
  ```

> With these volumes, your keys, database, and RabbitMQ state survive container restarts.

---

happy hacking <3
