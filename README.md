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
